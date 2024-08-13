package subscriberdatamanagement

import (
	"net/http"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/udm/internal/logger"
	"github.com/free5gc/udm/internal/sbi/producer"
	"github.com/free5gc/util/httpwrapper"
	"github.com/gin-gonic/gin"
)

// get group ids via External group Id or Internal group Id
func HTTPGetGroupIdentifiers(c *gin.Context) {
	req := httpwrapper.NewRequest(c.Request, nil)
	req.Query.Set("supported-features", c.Query("supported-features"))
	req.Query.Set("af-id", c.Query("af-id"))
	req.Query.Set("ext-groud-id", c.Query("ext-groud-id"))
	req.Query.Set("int-groud-id", c.Query("int-groud-id"))
	req.Query.Set("ue-id-ind", c.Query("ue-id-ind"))

	rsp := producer.HandleGetGroupIdentifiers(req)

	responseBody, err := openapi.Serialize(rsp.Body, "application/json")
	if err != nil {
		logger.SdmLog.Errorln(err)
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
