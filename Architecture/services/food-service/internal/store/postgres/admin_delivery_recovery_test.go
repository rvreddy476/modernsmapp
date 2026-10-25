package postgres
import("context";"errors";"sync";"testing";"github.com/google/uuid")

func TestAdminCodeReissueIsStageBoundAuditedAndIdempotent(t *testing.T){
 s,cleanup:=foodTestStore(t);defer cleanup();ctx:=context.Background()
 order,_,_:=seedOrderWithItem(t,s,"DELIVERY_ASSIGNED");_,partner:=seedDeliveryPartner(t,s)
 assignment:=seedDeliveryAssignmentWithStatus(t,s,order,&partner,"ACCEPTED")
 if _, err := s.db.Exec(ctx,`UPDATE food.delivery_assignments SET pickup_code='1234',delivery_code='5678',pickup_code_failed_attempts=5 WHERE id=$1`,assignment); err != nil {
 	t.Fatal(err)
 }
 admin,key:=uuid.New(),uuid.NewString();var wg sync.WaitGroup;errs:=make(chan error,5)
 for i:=0;i<5;i++{wg.Add(1);go func(){defer wg.Done();_,err:=s.AdminReissueDeliveryCode(ctx,admin,order,"pickup","Confirmed restaurant and rider",key);errs<-err}()};wg.Wait();close(errs)
 for err:=range errs{if err!=nil{t.Fatal(err)}}
 var code,drop,status string;var attempts int
 if err:=s.db.QueryRow(ctx,`SELECT pickup_code,delivery_code,pickup_code_failed_attempts,status::text FROM food.delivery_assignments WHERE id=$1`,assignment).Scan(&code,&drop,&attempts,&status);err!=nil{t.Fatal(err)}
 if code=="1234"||len(code)!=4||drop!="5678"||attempts!=0||status!="ACCEPTED"{t.Fatal("reissue modified the wrong state")}
 var n int;if err := s.db.QueryRow(ctx,`SELECT COUNT(*) FROM food.admin_audit_logs WHERE entity_id=$1 AND action='delivery.verification_reissued'`,order).Scan(&n); err != nil { t.Fatal(err) };if n!=1{t.Fatalf("audit count %d",n)}
 var leaked bool;if err := s.db.QueryRow(ctx,`SELECT COUNT(*),bool_or(payload ? 'code' OR payload ? 'pickup_code' OR payload ? 'delivery_code') FROM food.outbox_events WHERE event_type='food.order.verification_reissued' AND payload->>'order_id'=$1`,order.String()).Scan(&n,&leaked); err != nil { t.Fatal(err) };if n!=1||leaked{t.Fatalf("outbox count=%d leaked=%v",n,leaked)}
 if _,err:=s.AdminReissueDeliveryCode(ctx,admin,order,"pickup","duplicate different key",uuid.NewString());!errors.Is(err,ErrDeliveryRecoveryConflict){t.Fatalf("unlocked reissue %v",err)}
 if _,err:=s.AdminReissueDeliveryCode(ctx,admin,order,"delivery","wrong stage",uuid.NewString());!errors.Is(err,ErrDeliveryRecoveryConflict){t.Fatalf("wrong stage %v",err)}
 if _,err:=s.AdminReissueDeliveryCode(ctx,admin,order,"pickup","",uuid.NewString());!errors.Is(err,ErrDeliveryRecoveryInvalid){t.Fatalf("empty reason %v",err)}
 assertOrderStatus(t,s,order,"DELIVERY_ASSIGNED")
}

func TestAdminDropCodeReissueNeverCompletesOrderAndCannotRaceTwice(t *testing.T){
 s,cleanup:=foodTestStore(t);defer cleanup();ctx:=context.Background()
 order,_,_:=seedOrderWithItem(t,s,"OUT_FOR_DELIVERY");_,partner:=seedDeliveryPartner(t,s)
 assignment:=seedDeliveryAssignmentWithStatus(t,s,order,&partner,"PICKED_UP")
 if _, err := s.db.Exec(ctx,`UPDATE food.delivery_assignments SET pickup_code='1234',delivery_code='5678',delivery_code_failed_attempts=5 WHERE id=$1`,assignment); err != nil {
 	t.Fatal(err)
 }
 var wg sync.WaitGroup;errs:=make(chan error,2)
 for i:=0;i<2;i++{wg.Add(1);go func(){defer wg.Done();_,err:=s.AdminReissueDeliveryCode(ctx,uuid.New(),order,"delivery","Confirmed recipient",uuid.NewString());errs<-err}()};wg.Wait();close(errs)
 ok,conflicts:=0,0;for err:=range errs{if err==nil{ok++}else if errors.Is(err,ErrDeliveryRecoveryConflict){conflicts++}else{t.Fatal(err)}}
 if ok!=1||conflicts!=1{t.Fatalf("winners=%d conflicts=%d",ok,conflicts)}
 assertOrderStatus(t,s,order,"OUT_FOR_DELIVERY")
}

func TestAdminDeliveryRecoveryShowsRealExceptions(t *testing.T){
 s,cleanup:=foodTestStore(t);defer cleanup();ctx:=context.Background()
 waiting,_,_:=seedOrderWithItem(t,s,"DELIVERY_ASSIGNING")
 if _, err := s.db.Exec(ctx,`UPDATE food.orders SET placed_at=NOW()-INTERVAL '5 minutes' WHERE id=$1`,waiting); err != nil {
 	t.Fatal(err)
 }
 fresh,_,_:=seedOrderWithItem(t,s,"DELIVERY_ASSIGNING")
 locked,_,_:=seedOrderWithItem(t,s,"OUT_FOR_DELIVERY");_,partner:=seedDeliveryPartner(t,s)
 assignment:=seedDeliveryAssignmentWithStatus(t,s,locked,&partner,"PICKED_UP")
 if _, err := s.db.Exec(ctx,`UPDATE food.delivery_assignments SET delivery_code_failed_attempts=5 WHERE id=$1`,assignment); err != nil {
 	t.Fatal(err)
 }
 rows,err:=s.AdminDeliveryRecovery(ctx,Pagination{Limit:500});if err!=nil{t.Fatal(err)}
 found:=map[uuid.UUID]string{};for _,r:=range rows{found[r.OrderID]=r.Issue}
 if found[waiting]!="unassigned"||found[locked]!="delivery_code_locked"||found[fresh]!=""{t.Fatalf("queue classifications %q/%q/%q",found[waiting],found[locked],found[fresh])}
}
