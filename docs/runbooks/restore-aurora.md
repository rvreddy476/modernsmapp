# Aurora PostgreSQL: restore to a point in time

**Status: UNTESTED.** Written 16 November 2026 from the Terraform configuration. No restore has been done in any environment. Prove it with the drill below on QA and fill in the record at the end before relying on it.

What exists (as code, `infra/terraform/modules/aurora`, `modules/ops-baseline`):

- Cluster `atpost-<env>-aurora`, databases `app`, `identity_db`, `chat_db`, `call_db`, `commerce_db`.
- Continuous backup with point-in-time restore: 7 days on production (`aurora_backup_retention_days`), the value in `qa.tfvars` on QA.
- AWS Backup: one snapshot a day at 19:30 UTC, kept 14 days, vault `atpost-<env>-vault`.

A restore always creates a NEW cluster next to the old one. Nothing here changes the running cluster until Part C.

Run every command in Git Bash, with AWS credentials for the environment's account. Needs `aws`, `kubectl` and `node` on the PATH. `ENV` is `qa` or `prod`.

## Part A. Restore drill (QA): restore, compare row counts, delete

1. Set the variables.
   ```bash
   export ENV=qa
   export AWS_REGION=ap-south-1
   export SRC=atpost-$ENV-aurora
   export DST=atpost-$ENV-aurora-drill-$(date -u +%Y%m%d)
   aws eks update-kubeconfig --name atpost-$ENV --region $AWS_REGION
   ```
2. Read the connection details into shell variables. Nothing is printed.
   ```bash
   SECRET=$(aws secretsmanager get-secret-value --secret-id atpost/$ENV/aurora/master --query SecretString --output text)
   export PGUSER=$(node -pe 'JSON.parse(require("fs").readFileSync(0,"utf8")).username' <<<"$SECRET")
   export PGPASSWORD=$(node -pe 'JSON.parse(require("fs").readFileSync(0,"utf8")).password' <<<"$SECRET")
   export SRC_HOST=$(node -pe 'JSON.parse(require("fs").readFileSync(0,"utf8")).host' <<<"$SECRET")
   unset SECRET
   ```
3. The row-count query is `docs/runbooks/restore-aurora-counts.sql` (read-only, one line per table). Point a variable at it; run from the repository root.
   ```bash
   export COUNTS_SQL=docs/runbooks/restore-aurora-counts.sql
   ```
4. Note the restore time T0, then count every table of every database on the SOURCE straight away. The pod is deleted when the command ends.
   ```bash
   export T0=$(date -u +%FT%TZ); echo "T0=$T0"
   for db in app identity_db chat_db call_db commerce_db; do
     kubectl -n atpost run pg-drill-$RANDOM --rm -i --restart=Never --quiet \
       --image=postgres:16-alpine --env="PGPASSWORD=$PGPASSWORD" -- \
       psql "host=$SRC_HOST user=$PGUSER dbname=$db sslmode=require" -At -F $'\t' -f - \
       < "$COUNTS_SQL" > aurora-counts-source-$db.tsv
     wc -l aurora-counts-source-$db.tsv
   done
   ```
   Expected: five files, each with one line per table.
5. Wait until T0 is restorable (about 5 minutes).
   ```bash
   aws rds describe-db-clusters --db-cluster-identifier $SRC \
     --query 'DBClusters[0].LatestRestorableTime' --output text
   ```
   Repeat until the time printed is later than T0.
6. Read the network settings of the source and start the restore. Start the clock.
   ```bash
   SUBNET_GROUP=$(aws rds describe-db-clusters --db-cluster-identifier $SRC --query 'DBClusters[0].DBSubnetGroup' --output text)
   SG=$(aws rds describe-db-clusters --db-cluster-identifier $SRC --query 'DBClusters[0].VpcSecurityGroups[0].VpcSecurityGroupId' --output text)
   PARAMS=$(aws rds describe-db-clusters --db-cluster-identifier $SRC --query 'DBClusters[0].DBClusterParameterGroup' --output text)
   date -u +%FT%TZ
   aws rds restore-db-cluster-to-point-in-time \
     --source-db-cluster-identifier $SRC \
     --db-cluster-identifier $DST \
     --restore-to-time "$T0" \
     --db-subnet-group-name "$SUBNET_GROUP" \
     --vpc-security-group-ids "$SG" \
     --db-cluster-parameter-group-name "$PARAMS" \
     --serverless-v2-scaling-configuration MinCapacity=0.5,MaxCapacity=2 \
     --no-deletion-protection \
     --tags Key=Purpose,Value=restore-drill
   ```
7. A restored cluster has no instance. Add one and wait.
   ```bash
   aws rds create-db-instance --db-instance-identifier $DST-writer \
     --db-cluster-identifier $DST --db-instance-class db.serverless --engine aurora-postgresql
   aws rds wait db-instance-available --db-instance-identifier $DST-writer
   date -u +%FT%TZ
   export DST_HOST=$(aws rds describe-db-clusters --db-cluster-identifier $DST --query 'DBClusters[0].Endpoint' --output text)
   ```
   The second time printed minus the first is the time to restore (expect 15 to 30 minutes).
8. Count every table on the RESTORED cluster. The user and password are the source's.
   ```bash
   for db in app identity_db chat_db call_db commerce_db; do
     kubectl -n atpost run pg-drill-$RANDOM --rm -i --restart=Never --quiet \
       --image=postgres:16-alpine --env="PGPASSWORD=$PGPASSWORD" -- \
       psql "host=$DST_HOST user=$PGUSER dbname=$db sslmode=require" -At -F $'\t' -f - \
       < "$COUNTS_SQL" > aurora-counts-restored-$db.tsv
   done
   ```
9. Compare.
   ```bash
   for db in app identity_db chat_db call_db commerce_db; do
     echo "== $db"; diff aurora-counts-source-$db.tsv aurora-counts-restored-$db.tsv && echo MATCH
   done
   ```
   Expected: `MATCH` for every database. A table that was being written during step 4 may differ by a few rows; write each such table and both numbers in the record. A missing table, or a difference in a table nobody was writing, is a failed drill.
10. Delete the drill cluster.
    ```bash
    aws rds delete-db-instance --db-instance-identifier $DST-writer --skip-final-snapshot
    aws rds wait db-instance-deleted --db-instance-identifier $DST-writer
    aws rds delete-db-cluster --db-cluster-identifier $DST --skip-final-snapshot
    unset PGPASSWORD
    ```
11. Fill in the drill record below and commit it with the ten `.tsv` files' line counts (not the files).

## Part B. Restore from the daily AWS Backup snapshot instead

Use this when the point in time needed is older than the continuous-backup window but within 14 days.

1. List the recovery points and copy the ARN of the one wanted.
   ```bash
   aws backup list-recovery-points-by-backup-vault --backup-vault-name atpost-$ENV-vault \
     --query 'RecoveryPoints[].[CreationDate,RecoveryPointArn]' --output text | sort
   ```
2. Set `DST`, `SUBNET_GROUP`, `SG` and `PARAMS` (Part A step 1 and the first three lines of step 6), then restore the recovery point as a new cluster.
   ```bash
   aws rds restore-db-cluster-from-snapshot \
     --db-cluster-identifier $DST --snapshot-identifier "<the whole recovery point ARN>" \
     --engine aurora-postgresql \
     --db-subnet-group-name "$SUBNET_GROUP" --vpc-security-group-ids "$SG" \
     --db-cluster-parameter-group-name "$PARAMS" \
     --serverless-v2-scaling-configuration MinCapacity=0.5,MaxCapacity=2
   ```
3. Continue from Part A step 7.

## Part C. Real incident: put the restored cluster into service

Do this only with the lead present. It causes a full outage of every service for its duration.

1. Decide the restore time T0 (the last moment the data was good) and do Part A steps 1, 2, 6 and 7 with `export DST=atpost-$ENV-aurora-restored` and that T0. Remove `--no-deletion-protection` from step 6.
2. Stop every service so nothing writes to the old cluster.
   ```bash
   kubectl -n atpost scale deploy --all --replicas=0
   ```
3. Rename the old cluster out of the way, then give the restored cluster the old name. The endpoint name follows the cluster name, so no secret or values file changes.
   ```bash
   aws rds modify-db-cluster --db-cluster-identifier $SRC --new-db-cluster-identifier $SRC-broken --apply-immediately
   aws rds wait db-cluster-available --db-cluster-identifier $SRC-broken
   aws rds modify-db-cluster --db-cluster-identifier $DST --new-db-cluster-identifier $SRC --apply-immediately
   aws rds wait db-cluster-available --db-cluster-identifier $SRC
   ```
4. Check the endpoint answers (repeat Part A step 4 for database `app`; DNS can take a few minutes).
5. Start the services again: sync every application in Argo CD (`argocd app sync -l atpost.io/env=$ENV`), which restores the replica counts.
6. Check the edge answers, then sign in to the app and load the home feed. (`scripts/smoke/dev-smoke.sh` refuses any host that is not a dev host, so there is no scripted smoke for QA or production yet.)
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' https://api-qa.cleestudio.com/readyz
   curl -s -o /dev/null -w '%{http_code}\n' https://api-qa.cleestudio.com/v1/features
   ```
   Expected: `200` twice.
7. Tell the lead: Terraform's state still describes the old cluster and its instances. Do not run `terraform apply` on this environment until the state has been repaired (import of the renamed cluster and the new instance). Keep `$SRC-broken` until then.
8. Everything written after T0 is lost. Scylla and Redis were not rolled back, so likes, counters and timelines may refer to posts that no longer exist; the lead decides whether to restore those too.

## Drill record (copy this block for each drill)

```
Aurora restore drill
Date (UTC):                    
Environment:                   qa
Run by:                        
Method:                        point in time / AWS Backup snapshot
T0 (restore-to time, UTC):     
Restore started (UTC):         
Instance available (UTC):      
Time to restore:               

Database       tables   differing tables (name: source / restored)
app
identity_db
chat_db
call_db
commerce_db

Drill cluster deleted:         yes / no
Commands that had to be changed: 
Result:                        PASS / FAIL
Follow-ups:                    
```
