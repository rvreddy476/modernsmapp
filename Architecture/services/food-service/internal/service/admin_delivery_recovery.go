package service
import("context";"github.com/atpost/food-service/internal/store/postgres";"github.com/google/uuid")
func(s *Service) AdminDeliveryRecovery(ctx context.Context,p postgres.Pagination)([]postgres.DeliveryRecoveryRow,error){return s.store.AdminDeliveryRecovery(ctx,p)}
func(s *Service) AdminReissueDeliveryCode(ctx context.Context,admin,order uuid.UUID,kind,reason,key string)(map[string]any,error){
 result,err:=s.store.AdminReissueDeliveryCode(ctx,admin,order,kind,reason,key)
 if err==nil{s.publishRealtime(ctx,"food.order."+order.String(),"food.order.verification_reissued",result)}
 return result,err
}
