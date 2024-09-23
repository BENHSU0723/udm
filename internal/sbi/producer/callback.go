package producer

import (
	"fmt"
	"net/http"

	ben_models "github.com/BENHSU0723/openapi_public/models"
	"github.com/free5gc/openapi/models"
	udm_context "github.com/free5gc/udm/internal/context"
	"github.com/free5gc/udm/internal/logger"
	"github.com/free5gc/udm/internal/sbi/producer/callback"
	"github.com/free5gc/util/httpwrapper"
)

// HandleDataChangeNotificationToNFRequest ... Send Data Change Notification
func HandleDataChangeNotificationToNFRequest(request *httpwrapper.Request) *httpwrapper.Response {
	// step 1: log
	logger.CallbackLog.Infof("Handle DataChangeNotificationToNF")

	// step 2: retrieve request
	dataChangeNotify := request.Body.(models.DataChangeNotify)
	supi := request.Params["supi"]

	problemDetails := callback.DataChangeNotificationProcedure(dataChangeNotify.NotifyItems, supi)

	// step 4: process the return value from step 3
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
	}
}

// HandleDataChangeNotificationToNFRequest ... Send Data Change Notification
func HandlePostVn5gGroupSubscription(request *httpwrapper.Request) *httpwrapper.Response {
	logger.CallbackLog.Infof("Handle PostVn5gGroupSubscription")

	groupConfigSubs := request.Body.(ben_models.Vn5gGroupConfigSubscription)
	groupId := request.Params["groupId"]
	logger.CallbackLog.Warnln("group ID: ", groupId)
	logger.CallbackLog.Warnln("Group Config Subscription: ", groupConfigSubs)

	udmSelf := udm_context.GetSelf()
	udmSelf.UdmVn5gGroupDataSubscriptions[groupId] = &groupConfigSubs

	/* Contains the URI of the newly created resource, according
	   to the structure: {apiRoot}/subscription-data/subs-to-notify/{subsId} */
	locationHeader := fmt.Sprintf("/vn5glan-subscriptions/subs/%s", groupId)
	headers := http.Header{}
	headers.Set("Location", locationHeader)

	return httpwrapper.NewResponse(http.StatusCreated, headers, groupConfigSubs)
}

// HandleDataChangeNotificationToNFRequest ... Send Data Change Notification
func PreHandleVn5gGroupDataChangeNotification(groupID string, resourceId string, patchItems []ben_models.PatchItem) {
	logger.CallbackLog.Warnf("PreHandleVn5gGroupDataChangeNotification for 5G Vn Group ID: %s\n", groupID)
	notifyItems := []models.NotifyItem{}
	changes := []models.ChangeItem{}

	for _, patchItem := range patchItems {
		change := models.ChangeItem{
			Op:        models.ChangeType(patchItem.Op),
			Path:      patchItem.Path,
			From:      patchItem.From,
			OrigValue: nil,
			NewValue:  patchItem.Value,
		}
		changes = append(changes, change)
	}

	notifyItem := models.NotifyItem{
		ResourceId: resourceId,
		Changes:    changes,
	}

	notifyItems = append(notifyItems, notifyItem)

	// go callback.SendVn5gGroupDataChangeNotification(notifyItems, groupID)
	callback.SendVn5gGroupDataChangeNotification(notifyItems, groupID)
}
