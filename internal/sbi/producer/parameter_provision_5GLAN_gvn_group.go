package producer

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"reflect"
	"time"

	ben_models "github.com/BENHSU0723/openapi_public/models"
	"github.com/free5gc/openapi"
	"github.com/free5gc/udm/internal/logger"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/util/httpwrapper"

	udm_context "github.com/free5gc/udm/internal/context"
	"github.com/free5gc/udm/internal/util"
	"github.com/free5gc/util/mongoapi"
)

const (
	extintGroupIDMapColl = "subscriptionData.extintGroupIDMap"
)

// Create -- parameter provision  of 5GVN Group creation
func HandleCreate5GLANGroupRequest(request *httpwrapper.Request, c *gin.Context, GroupConfig ben_models.Model5GvnGroupConfiguration) *httpwrapper.Response {

	// step 1: log
	logger.PpLog.Info("Handle CreateRequest of 5GLANGroup")

	// step 2: create a request to UDR_DM_Query to check whether the group members' IE  are correct
	problemDetails := checkVnGroupConfigIsValid(request, GroupConfig)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	// step 3 mapping External group ID to Internal group ID, that's 1-1 map relation.
	// UDM assign mapping relation then record it to database(MongoDB)
	extGroupId := request.Params["extGroupId"]
	result, err := ExtIntGroupIDMap(extGroupId, &GroupConfig)
	if !result {
		logger.PpLog.Errorf(err.Error())
		problemDetails := &models.ProblemDetails{
			Title:  "Insert GroupID failure",
			Status: http.StatusInternalServerError,
			Detail: err.Error(),
			Cause:  "INSERT_GROUPID_FAILURE",
		}
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	// step 4: create a request to UDR_DM_create to create a new 5GLAN VN Group
	problemDetails, _ = Handle5GLANCreateRequest(request, GroupConfig, extGroupId)
	if problemDetails != nil {
		//if create 5G VN group data fail, then delete mapping relation
		filterData := bson.M{"internalGroupId": GroupConfig.InternalGroupIdentifier}
		err := mongoapi.RestfulAPIDeleteOne(extintGroupIDMapColl, filterData)
		if err != nil {
			logger.PpLog.Errorln("delete group id  from DB error:", err.Error())
		}
		problemDetails.Cause = " CREATION_NOT_ALLOWED"
		return httpwrapper.NewResponse(http.StatusForbidden, nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusCreated, nil, nil)
	}
}

func Handle5GLANCreateRequest(request *httpwrapper.Request, GroupConfig ben_models.Model5GvnGroupConfiguration, extGroupId string) (*models.ProblemDetails, *ben_models.Model5GvnGroupConfiguration) {
	// getUdrURI by extGroupId
	clientAPI, err := createBenUDMClientToUDR(extGroupId)
	if err != nil {
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	}
	resGroupConfig, res, err := clientAPI.Individual5GvnGroup5GLanDocumentApi.Put5GLANGroupData(context.Background(), extGroupId, GroupConfig)
	if err != nil {
		logger.PpLog.Errorln("Handle5GLANCreateRequest err:" + err.Error())
		//err is create group Forbidden reason or API error reason
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		return problemDetails, nil
	} else if len(resGroupConfig.Members) != len(GroupConfig.Members) {
		logger.PpLog.Errorln("Handle5GLANCreateRequest err: return group config invalid!")
		//if call API to UDM to Create Group successfully, return group config
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  "UDM_CREATE_GROUP_UDR_ERROR",
			Detail: "UDM call UDR api reutrn success(200), but return Group Config value error.",
		}
		return problemDetails, nil
	}

	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PpLog.Errorf("ModifyPpData response body cannot close: %+v", rspCloseErr)
		}
	}()

	return nil, &resGroupConfig
}

func checkVnGroupConfigIsValid(request *httpwrapper.Request, groupConfig ben_models.Model5GvnGroupConfiguration) (problemDetails *models.ProblemDetails) {

	// Step1: check all UEs have subscription datas in UDR, which means valid
	members := groupConfig.Members
	for _, memberUEid := range members {
		clientAPI, err := createBenUDMClientToUDR(memberUEid)
		if err != nil {
			logger.PpLog.Errorln("createBenUDMClientToUDR error: " + err.Error())
			return util.ProblemDetailsSystemFailure(err.Error())
		}

		//check each group member already have subscriptiondata, or the creation will fail
		_, res, err := clientAPI.DefaultApi.PolicyDataUesUeIdAmDataGet(context.Background(), memberUEid)
		if err != nil {
			problemDetails = &models.ProblemDetails{
				Status: int32(res.StatusCode),
				Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
				Detail: err.Error() + " [or] At Least One of Group Members have no Subscription Data",
			}
			logger.PpLog.Errorln(problemDetails.Detail)
			return
		}

		defer func() {
			if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
				logger.PpLog.Errorf("ModifyPpData response body cannot close: %+v", rspCloseErr)
			}
		}()
	}
	logger.PpLog.Info("Authentication sucess!!, each member have subscribed")
	return nil
}

func ExtIntGroupIDMap(extGroupId string, groupConfig *ben_models.Model5GvnGroupConfiguration) (bool, error) {

	// Connect to MongoDB
	if err := mongoapi.SetMongoDB("free5gc", "mongodb://localhost:27017"); err != nil {
		logger.PpLog.Errorf("Connect mongoDB to Map Group ID: %+v", err)
	}

	ueId := ""
	if len(groupConfig.Members) != 0 {
		ueId = groupConfig.Members[0]
	}

	//cnt size is flexible
	for cnt := 0; cnt < 10; cnt++ {
		internalGroupId := InterGroupIdGenerator(ueId, groupConfig.Var5gVnGroupData.AppDescriptors)
		groupConfig.InternalGroupIdentifier = internalGroupId
		filterData := bson.M{"externalGroupId": extGroupId}

		//check external group ID has been used or not
		dupExtIDdata, err := mongoapi.RestfulAPIGetOne(extintGroupIDMapColl, filterData)
		if err != nil {
			return false, err
		} else if len(dupExtIDdata) != 0 {
			return false, errors.New("External ID duplicated!!, Please change it.")
		}

		//put external/internal Group ID maping relation to mongoDB,and check interal ID used or not.
		filterData = bson.M{"internalGroupId": internalGroupId}
		mapData := bson.M{"externalGroupId": extGroupId, "internalGroupId": internalGroupId}
		result, err := mongoapi.RestfulAPIPutOne(extintGroupIDMapColl, filterData, mapData)
		if err != nil {
			return false, err
		} else if result {
			return false, errors.New("IntGroupID duplicated: " + internalGroupId)
		} else {
			return true, nil //success insert to mongoDB
		}
	}
	return false, errors.New("ExtIntGroupIDMap random generator error")
}

func InterGroupIdGenerator(ueId string, appDescList []ben_models.AppDescriptor) (intGroupID string) {
	//Group ID Pattern: '^[A-Fa-f0-9]{8}-[0-9]{3}-[0-9]{2,3}-([A-Fa-f0-9][A-Fa-f0-9]){1,10}$'.
	const letterBytes = "abcdefABCDEF0123456789"
	var letterRunes = []rune(letterBytes)

	//get plmn id from udm config file
	var serviceID string
check:
	for _, appDes := range appDescList {
		//servie name as key
		for serType, _ := range appDes.AppIds {
			if udm_context.GetSelf().Vn5glanServiceType[serType] != "" {
				serviceID = udm_context.GetSelf().Vn5glanServiceType[serType]
				break check
			}
		}
	}
	if serviceID == "" {
		serviceID = "AAA00001"
	}
	plmn := udm_context.GetSelf().Plmn

	intGroupID += serviceID
	intGroupID += "-"
	intGroupID += plmn.Mcc //MCC
	intGroupID += "-"
	intGroupID += plmn.Mnc //MNC
	intGroupID += "-"
	intGroupID += randStringRunes(10, letterRunes) //size is flexible between 1 to 10, even size only
	return
}
func randStringRunes(n int, runes []rune) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]rune, n)
	for i := range b {
		b[i] = runes[r.Intn(len(runes))]
	}
	return string(b)
}

func HandleDelete5gVnGroupRequest(request *httpwrapper.Request, c *gin.Context) *httpwrapper.Response {
	logger.VnGroupLog.Infoln("Handle delete a 5G VN Group configuration")

	extGroupId := request.Params["extGroupId"]
	// refer to  TS 29.571 V17.8.0, Table 5.3.2-1: Simple Data Types
	// MtcProviderInformation: String uniquely identifying MTC provider information.
	mtcPvdInfo := request.Query.Get("mtc-provider-info")
	afId := request.Query.Get("af-id")

	problemDetails := Delete5gVnGroupProcedure(extGroupId, mtcPvdInfo, afId)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusNoContent, nil, problemDetails)
	}
}

func Delete5gVnGroupProcedure(extGroupId, mtcPvdInfo, afId string) *models.ProblemDetails {
	// find UDR URI
	clientAPI, err := createBenUDMClientToUDR(extGroupId)
	if err != nil {
		logger.VnGroupLog.Errorln("createBenUDMClientToUDR error: " + err.Error())
		return util.ProblemDetailsSystemFailure(err.Error())
	}

	res, err := clientAPI.Individual5GvnGroup5GLanDocumentApi.Delete5GLANGroupData(context.Background(), extGroupId)
	// logger.VnGroupLog.Info("extGroupId:", extGroupId,"vnGroupConfig:", vnGroupConfig)
	if err != nil {
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		logger.VnGroupLog.Errorln(problemDetails.Detail)
		return problemDetails
	}

	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.VnGroupLog.Errorf("Delete5GLANGroupData response body cannot close: %+v", rspCloseErr)
		}
	}()

	return nil
}

func HandleModify5GLANGroupRequest(request *httpwrapper.Request, c *gin.Context, vn5gGpCfg ben_models.Model5GvnGroupConfiguration) *httpwrapper.Response {
	logger.VnGroupLog.Infoln("Handle Modify a 5G VN Group configuration")

	// refer to  TS 29.571 V17.8.0, Table 5.3.2-1: Simple Data Types
	extGroupId := request.Params["extGroupId"]
	suppFeat := request.Params["supported-features"]

	// first of all, check the validity of new VN group config
	problemDetails := checkVnGroupConfigIsValid(request, vn5gGpCfg)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	}

	patchResult, problemDetails := Modify5gVnGroupProcedure(extGroupId, suppFeat, vn5gGpCfg)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		if patchResult != nil {
			return httpwrapper.NewResponse(http.StatusOK, nil, problemDetails)
		} else {
			return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
		}
	}
}

func Modify5gVnGroupProcedure(extGroupId, suppFeat string, newVn5gGpCfg ben_models.Model5GvnGroupConfiguration) (*ben_models.PatchResult, *models.ProblemDetails) {
	logger.VnGroupLog.Infoln("Enter Modify5gVnGroupProcedure")
	// different from general index is ueId, here using external group id as UDR searching id
	clientAPI, err := createBenUDMClientToUDR(extGroupId)
	if err != nil {
		logger.VnGroupLog.Errorln("createBenUDMClientToUDR error: " + err.Error())
		return nil, util.ProblemDetailsSystemFailure(err.Error())
	}

	// Get 5G LAN VN Group config from UDR
	oriGpCfg, res, err := clientAPI.Individual5GvnGroup5GLanDocumentApi.Get5GLANGroupData(context.Background(), extGroupId)
	if err != nil {
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		logger.VnGroupLog.Errorln(problemDetails.Detail)
		return nil, problemDetails
	} else if reflect.DeepEqual(oriGpCfg, newVn5gGpCfg) {
		logger.VnGroupLog.Infoln("the group config doesn't change, so directly response")
		return nil, nil
	}

	// compare the origin group config and new group condfig, and make patch item list
	patchItems, _ := makeGpPatchItemsForUDR(oriGpCfg, newVn5gGpCfg)

	// Patch a group config throug UDR
	patchResult, res, err := clientAPI.Individual5GvnGroup5GLanDocumentApi.Patch5GLANGroupData(context.Background(), extGroupId, patchItems, suppFeat)
	if err != nil {
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		logger.VnGroupLog.Errorln(problemDetails.Detail)
		return nil, problemDetails
	}

	// IE: Multicast Group, It's a important IE that should be notified to SMF when it changed
	// Notify SMF with Multicast Data Changed if Motify operation with UDR successes
	if !reflect.DeepEqual(oriGpCfg.MulticastGroupList, newVn5gGpCfg.MulticastGroupList) &&
		(res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK) {
		patchItemsMulcst := makeGpPatchItemsForSMF(oriGpCfg, newVn5gGpCfg)
		if len(patchItemsMulcst) != 0 {
			PreHandleVn5gGroupDataChangeNotification(oriGpCfg.InternalGroupIdentifier, "no-content", patchItemsMulcst)
		}
	}

	return &patchResult, nil
}

// only included Multicast group data changed of a single VN 5G group
func makeGpPatchItemsForSMF(oriGpCfg, newVn5gGpCfg ben_models.Model5GvnGroupConfiguration) []ben_models.PatchItem {
	multicastGpItems := make([]ben_models.PatchItem, 0)
	// Currently make 'add' items for SMF
	// TODO: add other PatchOperation Types
	for _, newMultiGp := range newVn5gGpCfg.MulticastGroupList {
		isNewGroup := true
		for _, oldMultiGp := range oriGpCfg.MulticastGroupList {
			if oldMultiGp.MultiGroupId == newMultiGp.MultiGroupId {
				isNewGroup = false
				break
			}
		}
		// if it's a new created multicast group, then it sould been notified
		if isNewGroup {
			multicastGpItems = append(multicastGpItems, ben_models.PatchItem{
				Op:    ben_models.PatchOperation_ADD,
				Path:  "/multicastGroupList/-",
				Value: newMultiGp,
			})
		}
	}
	return multicastGpItems
}

func makeGpPatchItemsForUDR(oriGpCfg, newVn5gGpCfg ben_models.Model5GvnGroupConfiguration) ([]ben_models.PatchItem, []ben_models.ReportItem) {
	// refer to TS23.501 v17.7.0 section 5.29.2 5G VN group management, The PDU session type, DNN, S-NSSAI
	// provided within 5G VN group data cannot be modified after the initial provisioning.
	var forbiddenItems []ben_models.ReportItem
	if !reflect.DeepEqual(oriGpCfg.Var5gVnGroupData.PduSessionTypes, newVn5gGpCfg.Var5gVnGroupData.PduSessionTypes) {
		forbiddenItems = append(forbiddenItems, ben_models.ReportItem{
			Path:   "/5gVnGroupData/pduSessionTypes/-",
			Reason: "refer to TS23.501 v17.7.0 section 5.29.2 5G VN group management,the PDU session type, DNN, S-NSSAI cannot be modified after the initial provisioning.",
		})
	}
	if oriGpCfg.Var5gVnGroupData.Dnn != newVn5gGpCfg.Var5gVnGroupData.Dnn {
		forbiddenItems = append(forbiddenItems, ben_models.ReportItem{
			Path:   "/5gVnGroupData/dnn",
			Reason: "refer to TS23.501 v17.7.0 section 5.29.2 5G VN group management,the PDU session type, DNN, S-NSSAI cannot be modified after the initial provisioning.",
		})
	}
	if !reflect.DeepEqual(oriGpCfg.Var5gVnGroupData.SNssai, newVn5gGpCfg.Var5gVnGroupData.SNssai) {
		forbiddenItems = append(forbiddenItems, ben_models.ReportItem{
			Path:   "/5gVnGroupData/sNssai",
			Reason: "refer to TS23.501 v17.7.0 section 5.29.2 5G VN group management,the PDU session type, DNN, S-NSSAI cannot be modified after the initial provisioning.",
		})
	}

	var patchItems []ben_models.PatchItem
	if !reflect.DeepEqual(oriGpCfg.Members, newVn5gGpCfg.Members) {
		// for UDR used, it's used for ADD internal group id to am/sm subscription data
		var addUsers []string
		for _, newUser := range newVn5gGpCfg.Members {
			for i, oldUser := range oriGpCfg.Members {
				if newUser == oldUser {
					break
				} else if i == len(oriGpCfg.Members)-1 {
					addUsers = append(addUsers, newUser)
				}
			}
		}
		if len(addUsers) != 0 {
			patchItems = append(patchItems, ben_models.PatchItem{
				Op:    ben_models.PatchOperation_ADD,
				Path:  "/internalGroupIds",
				Value: addUsers,
			})
		}

		// for UDR used, it's used for DELETE internal group id to am/sm subscription data
		var rmUser []string
		for _, oldUser := range oriGpCfg.Members {
			for i, newUser := range newVn5gGpCfg.Members {
				if oldUser == newUser {
					break
				} else if i == len(newVn5gGpCfg.Members)-1 {
					rmUser = append(rmUser, oldUser)
				}
			}
		}
		if len(rmUser) != 0 {
			patchItems = append(patchItems, ben_models.PatchItem{
				Op:    ben_models.PatchOperation_REMOVE,
				Path:  "/internalGroupIds",
				Value: rmUser,
			})
		}

		// UDR can directly used it to replcae the members info.
		if !reflect.DeepEqual(oriGpCfg.Members, newVn5gGpCfg.Members) {
			patchItems = append(patchItems, ben_models.PatchItem{
				Op:    ben_models.PatchOperation_REPLACE,
				Path:  "/members",
				Value: newVn5gGpCfg.Members,
			})
		}
	}

	// Multicast group Info
	if !reflect.DeepEqual(oriGpCfg.MulticastGroupList, newVn5gGpCfg.MulticastGroupList) {
		patchItems = append(patchItems, ben_models.PatchItem{
			Op:    ben_models.PatchOperation_REPLACE,
			Path:  "/multicastGroupList",
			Value: newVn5gGpCfg.MulticastGroupList,
		})
	}

	logger.VnGroupLog.Warnf("Patch Item List:%+v\n", patchItems)

	// this way still works, but UDR need to seperate add and delete to remove internal group id from sm/am subs data
	// patchItems = append(patchItems, models.PatchItem{
	// 	Op:    models.PatchOperation_ADD,
	// 	Path:  "/members",
	// 	Value: newVn5gGpCfg.Members,
	// })

	// TODO: add other IEs to patchItems
	return patchItems, forbiddenItems
}

func HandleGet5GLANGroupDataRequest(request *httpwrapper.Request) *httpwrapper.Response {
	logger.VnGroupLog.Info("Handle Get5GLANGroupDataRequest")

	extGroupId := request.Params["extGroupId"]
	response, problemDetails := HandleGet5GLANGroupDataProcedure(extGroupId)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	}
}
func HandleGet5GLANGroupDataProcedure(extGroupId string) (*ben_models.Model5GvnGroupConfiguration, *models.ProblemDetails) {

	//different from general index is ueId, here using external group id as UDR searching id
	clientAPI, err := createBenUDMClientToUDR(extGroupId)
	if err != nil {
		logger.VnGroupLog.Errorln("createBenUDMClientToUDR error: " + err.Error())
		return nil, util.ProblemDetailsSystemFailure(err.Error())
	}

	//Get 5G LAN VN Group config from UDR
	vnGroupConfig, res, err := clientAPI.Individual5GvnGroup5GLanDocumentApi.Get5GLANGroupData(context.Background(), extGroupId)
	// logger.VnGroupLog.Info("extGroupId:", extGroupId,"vnGroupConfig:", vnGroupConfig)
	if err != nil {
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		logger.VnGroupLog.Errorln(problemDetails.Detail)
		return nil, problemDetails
	}

	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.VnGroupLog.Errorf("VN5GLANgroupDataGet response body cannot close: %+v", rspCloseErr)
		}
	}()

	return &vnGroupConfig, nil
}

func HandleGetParameterProvisionPerAfRequest(request *httpwrapper.Request) *httpwrapper.Response {
	logger.VnGroupLog.Info("Handle GetParameterProvisionPerAfRequest")

	ueId := request.Params["ueId"]
	afInstanceId := request.Params["afInstanceId"]

	// refer to Table 6.5.3.4.2-1: Resource URI variables for this resource
	switch ueId {
	case "anyUE":
	// case single UE:
	// case extgroupid:
	default:
		return httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
	}

	response, problemDetails := HandleGetParameterProvisionPerAfProcedure(ueId, afInstanceId)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusOK, nil, response)
	}
}

func HandleGetParameterProvisionPerAfProcedure(ueId, afInsId string) (*ben_models.PpDataEntry, *models.ProblemDetails) {

	//different from general index is ueId, here using external group id as UDR searching id
	clientAPI, err := createBenUDMClientToUDR(ueId)
	if err != nil {
		logger.VnGroupLog.Errorln("createBenUDMClientToUDR error: " + err.Error())
		return nil, util.ProblemDetailsSystemFailure(err.Error())
	}

	//Get 5G LAN VN Group config from UDR
	ppDataEntry, res, err := clientAPI.ParameterProvisionEntryDocumentApi.GetppDataEntry(context.Background(), ueId, afInsId, nil)
	// logger.VnGroupLog.Info("extGroupId:", extGroupId,"vnGroupConfig:", vnGroupConfig)
	if err != nil {
		problemDetails := &models.ProblemDetails{
			Status: int32(res.StatusCode),
			Cause:  err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails).Cause,
			Detail: err.Error(),
		}
		logger.VnGroupLog.Errorln(problemDetails.Detail)
		return nil, problemDetails
	}

	return &ppDataEntry, nil
}
