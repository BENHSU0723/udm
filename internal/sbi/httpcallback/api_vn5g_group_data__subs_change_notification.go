package httpcallback

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BENHSU0723/openapi_public/models"
	"github.com/free5gc/openapi"
	"github.com/free5gc/udm/internal/logger"
	"github.com/free5gc/udm/internal/sbi/producer"
	"github.com/free5gc/util/httpwrapper"
)

func HTTPPostVn5gGroupConfigSubscriptions(c *gin.Context) {
	var vnGroupChangeNotify models.Vn5gGroupConfigSubscription
	// step 1: retrieve http request body
	requestBody, err := c.GetRawData()
	if err != nil {
		problemDetail := models.ProblemDetails{
			Title:  "System failure",
			Status: http.StatusInternalServerError,
			Detail: err.Error(),
			Cause:  "SYSTEM_FAILURE",
		}
		logger.CallbackLog.Errorf("Get Request Body error: %+v", err)
		c.JSON(http.StatusInternalServerError, problemDetail)
		return
	}

	// step 2: convert requestBody to openapi models
	err = openapi.Deserialize(&vnGroupChangeNotify, requestBody, "application/json")
	if err != nil {
		problemDetail := "[Request Body] " + err.Error()
		rsp := models.ProblemDetails{
			Title:  "Malformed request syntax",
			Status: http.StatusBadRequest,
			Detail: problemDetail,
		}
		logger.CallbackLog.Errorln(problemDetail)
		c.JSON(http.StatusBadRequest, rsp)
		return
	}

	req := httpwrapper.NewRequest(c.Request, vnGroupChangeNotify)
	req.Params["groupId"] = c.Params.ByName("groupId")

	rsp := producer.HandlePostVn5gGroupSubscription(req)
	responseBody, err := openapi.Serialize(rsp.Body, "application/json")
	if err != nil {
		logger.CallbackLog.Errorln(err)
		problemDetails := models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Cause:  "SYSTEM_FAILURE",
			Detail: err.Error(),
		}
		c.JSON(http.StatusInternalServerError, problemDetails)
	} else {
		c.Data(rsp.Status, "application/json", responseBody)
	}
}
