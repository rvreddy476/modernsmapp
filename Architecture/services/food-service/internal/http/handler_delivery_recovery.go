package http
import("errors";"net/http";"github.com/atpost/food-service/internal/store/postgres";"github.com/atpost/shared/api";"github.com/gin-gonic/gin";"github.com/google/uuid")
func(h *Handler) AdminDeliveryRecovery(c *gin.Context){
 if _,ok:=h.requireUser(c);!ok{return};page:=paginationFromQuery(c)
 rows,err:=h.svc.AdminDeliveryRecovery(c.Request.Context(),page)
 if err!=nil{api.ErrorWithContext(c.Request.Context(),c.Writer,http.StatusInternalServerError,"FOOD_RECOVERY_READ_FAILED","Delivery recovery queue could not be loaded",nil);return}
 api.JSONWithContext(c.Request.Context(),c.Writer,http.StatusOK,paginatedResponse(rows,page))
}
func(h *Handler) AdminReissueDeliveryCode(c *gin.Context){
 admin,ok:=h.requireUser(c);if !ok{return};order,err:=uuid.Parse(c.Param("orderId"));if err!=nil{api.ErrorWithContext(c.Request.Context(),c.Writer,400,"INVALID_ORDER_ID","Invalid order id",nil);return}
 var body struct{Kind string `json:"kind"`;Reason string `json:"reason"`}
 if err=c.ShouldBindJSON(&body);err!=nil{api.ErrorWithContext(c.Request.Context(),c.Writer,400,"INVALID_BODY","Verification type and reason required",nil);return}
 result,err:=h.svc.AdminReissueDeliveryCode(c.Request.Context(),admin,order,body.Kind,body.Reason,c.GetHeader("Idempotency-Key"))
 if err!=nil{
  if errors.Is(err,postgres.ErrDeliveryRecoveryInvalid){api.ErrorWithContext(c.Request.Context(),c.Writer,422,"FOOD_RECOVERY_INVALID",err.Error(),nil);return}
  if errors.Is(err,postgres.ErrDeliveryRecoveryConflict){api.ErrorWithContext(c.Request.Context(),c.Writer,409,"FOOD_RECOVERY_CONFLICT",err.Error(),nil);return}
  if writeKnownError(c,err){return};api.ErrorWithContext(c.Request.Context(),c.Writer,500,"FOOD_RECOVERY_FAILED","Verification code could not be reissued",nil);return
 }
 api.JSONWithContext(c.Request.Context(),c.Writer,200,result)
}
