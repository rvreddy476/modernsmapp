package postgres

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "strings"
 "github.com/google/uuid"
)

var ErrDeliveryRecoveryInvalid=errors.New("a valid verification type, reason and idempotency key are required")
var ErrDeliveryRecoveryConflict=errors.New("verification is not locked or the delivery stage no longer permits reissue")

type DeliveryRecoveryRow struct {
 OrderID uuid.UUID `json:"order_id"`
 OrderNumber string `json:"order_number"`
 RestaurantName string `json:"restaurant_name"`
 Status string `json:"status"`
 AssignmentStatus string `json:"assignment_status"`
 Issue string `json:"issue"`
 PlacedAt string `json:"placed_at"`
 OfferAttempts int `json:"offer_attempts"`
 LiveOffers int `json:"live_offers"`
}

// Only operational exceptions are shown; ordinary 20-second offers do not
// immediately become alerts. Nothing here invents an assignment or changes money.
func(s *Store) AdminDeliveryRecovery(ctx context.Context,page Pagination)([]DeliveryRecoveryRow,error){
 rows,err:=s.db.Query(ctx,`SELECT o.id,o.order_number,r.name,o.status::text,COALESCE(a.status::text,''),
 CASE WHEN a.pickup_code_failed_attempts>=5 AND o.status='DELIVERY_ASSIGNED' THEN 'pickup_code_locked'
 WHEN a.delivery_code_failed_attempts>=5 AND o.status IN ('PICKED_UP','OUT_FOR_DELIVERY') THEN 'delivery_code_locked'
 WHEN a.status='FAILED' THEN 'failed_delivery' ELSE 'unassigned' END,
 o.placed_at::text,
 (SELECT count(*)::int FROM food.delivery_offers f WHERE f.order_id=o.id),
 (SELECT count(*)::int FROM food.delivery_offers f WHERE f.order_id=o.id AND f.status='pending' AND f.expires_at>NOW())
 FROM food.orders o JOIN food.restaurants r ON r.id=o.restaurant_id
 LEFT JOIN food.delivery_assignments a ON a.order_id=o.id
 WHERE (o.status='DELIVERY_ASSIGNING' AND COALESCE((SELECT MAX(h.created_at) FROM food.order_status_history h WHERE h.order_id=o.id AND h.to_status='DELIVERY_ASSIGNING'),o.placed_at)<NOW()-INTERVAL '60 seconds')
 OR (o.status='DELIVERY_ASSIGNED' AND a.pickup_code_failed_attempts>=5)
 OR (o.status IN ('PICKED_UP','OUT_FOR_DELIVERY') AND a.delivery_code_failed_attempts>=5)
 OR (a.status='FAILED' AND o.status NOT IN ('DELIVERED','REFUNDED','CANCELLED_BY_CUSTOMER','CANCELLED_BY_ADMIN','CANCELLED_BY_RESTAURANT'))
 ORDER BY o.placed_at LIMIT $1 OFFSET $2`,page.Limit,page.Offset)
 if err!=nil{return nil,err};defer rows.Close();out:=[]DeliveryRecoveryRow{}
 for rows.Next(){var row DeliveryRecoveryRow;if err:=rows.Scan(&row.OrderID,&row.OrderNumber,&row.RestaurantName,&row.Status,&row.AssignmentStatus,&row.Issue,&row.PlacedAt,&row.OfferAttempts,&row.LiveOffers);err!=nil{return nil,err};out=append(out,row)}
 return out,rows.Err()
}

// Reissue rotates the secret rather than clearing a brute-force limit on the
// same code. The response, event and audit never disclose the code. Reissue is
// not a verification and cannot complete pickup or delivery.
func(s *Store) AdminReissueDeliveryCode(ctx context.Context,adminID,orderID uuid.UUID,kind,reason,key string)(map[string]any,error){
 reason=strings.TrimSpace(reason);key=strings.TrimSpace(key)
 if adminID==uuid.Nil||(kind!="pickup"&&kind!="delivery")||reason==""||len(reason)>500||key==""||len(key)>200{return nil,ErrDeliveryRecoveryInvalid}
 tx,err:=s.db.Begin(ctx);if err!=nil{return nil,err};defer tx.Rollback(ctx)
 body,handled,err:=s.lockGenericIdempotency(ctx,tx,adminID,key);if err!=nil{return nil,err}
 if handled {if fmt.Sprint(body["order_id"])!=orderID.String()||body["kind"]!=kind{return nil,ErrDeliveryRecoveryInvalid};return body,tx.Commit(ctx)}
 var os,as,oldCode string;var attempts int
 // The field is selected from a fixed enum, never from unchecked input.
 codeColumn,attemptColumn:="pickup_code","pickup_code_failed_attempts"
 if kind=="delivery"{codeColumn,attemptColumn="delivery_code","delivery_code_failed_attempts"}
 err=tx.QueryRow(ctx,`SELECT o.status::text,a.status::text,COALESCE(a.`+codeColumn+`,''),a.`+attemptColumn+`
 FROM food.delivery_assignments a JOIN food.orders o ON o.id=a.order_id WHERE a.order_id=$1 FOR UPDATE OF a`,orderID).Scan(&os,&as,&oldCode,&attempts)
 if err!=nil{return nil,err}
 allowed:=kind=="pickup"&&os=="DELIVERY_ASSIGNED"&&(as=="ACCEPTED"||as=="ARRIVED_AT_RESTAURANT")
 if kind=="delivery"{allowed=(os=="PICKED_UP"||os=="OUT_FOR_DELIVERY")&&(as=="PICKED_UP"||as=="ARRIVED_AT_CUSTOMER")}
 if !allowed||attempts<5{return nil,ErrDeliveryRecoveryConflict}
 code,err:=generateOTP(4);if err!=nil{return nil,err};for code==oldCode{code,err=generateOTP(4);if err!=nil{return nil,err}}
 if _,err=tx.Exec(ctx,`UPDATE food.delivery_assignments SET `+codeColumn+`=$2,`+attemptColumn+`=0 WHERE order_id=$1`,orderID,code);err!=nil{return nil,err}
 body=map[string]any{"order_id":orderID.String(),"kind":kind,"reissued":true}
 if err=writeAdminAudit(ctx,tx,adminID,"delivery.verification_reissued","order",&orderID,map[string]any{"kind":kind,"reason":reason,"previous_attempts":attempts});err!=nil{return nil,err}
 var customer,restaurantOwner,restaurantID uuid.UUID;var partnerUser *uuid.UUID
 if err=tx.QueryRow(ctx,`SELECT o.user_id,r.owner_user_id,p.user_id,r.id FROM food.orders o JOIN food.restaurants r ON r.id=o.restaurant_id JOIN food.delivery_assignments a ON a.order_id=o.id LEFT JOIN food.delivery_partners p ON p.id=a.delivery_partner_id WHERE o.id=$1`,orderID).Scan(&customer,&restaurantOwner,&partnerUser,&restaurantID);err!=nil{return nil,err}
 raw,_:=json.Marshal(map[string]any{"order_id":orderID,"user_id":customer,"restaurant_id":restaurantID,"restaurant_owner_user_id":restaurantOwner,"delivery_partner_user_id":partnerUser,"kind":kind})
 if err=foodOutbox.Enqueue(ctx,tx,"food.order.verification_reissued","food.order."+orderID.String(),raw);err!=nil{return nil,err}
 if err=s.completeGenericIdempotency(ctx,tx,adminID,key,200,body);err!=nil{return nil,err}
 return body,tx.Commit(ctx)
}
