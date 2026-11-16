# ElastiCache (Valkey): restore from a snapshot

**Status: UNTESTED.** Written 16 November 2026 from the Terraform configuration. No restore has been done in any environment. Prove it with the drill below on QA and fill in the record at the end before relying on it.

What exists (as code, `infra/terraform/modules/elasticache`):

- Replication group `atpost-<env>-valkey`, TLS and an AUTH token, encrypted with the key `alias/atpost-<env>-elasticache`.
- One automatic snapshot a day in the window 18:00 to 19:00 UTC, kept 5 days on production and 1 day on QA.

What is in it: like and reaction counters, the feed hydration cache, rate-limit counters, consumer dedupe receipts, sessions. Most keys expire on their own; counters are rebuilt from Scylla and Postgres by the services' reconcile jobs. A restore brings back the state at the snapshot, up to 24 hours old.

A snapshot can only be restored into a NEW replication group. Nothing here changes the running one until Part B.

Run every command in Git Bash, with AWS credentials for the environment's account. Needs `aws`, `kubectl` and `node` on the PATH. `ENV` is `qa` or `prod`.

## Part A. Restore drill (QA): restore, compare key counts, delete

1. Set the variables.
   ```bash
   export ENV=qa
   export AWS_REGION=ap-south-1
   export SRC=atpost-$ENV-valkey
   export DST=atpost-$ENV-valkey-drill
   aws eks update-kubeconfig --name atpost-$ENV --region $AWS_REGION
   ```
2. Read the AUTH token and the endpoint into shell variables. Nothing is printed.
   ```bash
   SECRET=$(aws secretsmanager get-secret-value --secret-id atpost/$ENV/elasticache/auth --query SecretString --output text)
   export REDISCLI_AUTH=$(node -pe 'JSON.parse(require("fs").readFileSync(0,"utf8")).auth_token' <<<"$SECRET")
   export SRC_HOST=$(node -pe 'JSON.parse(require("fs").readFileSync(0,"utf8")).primary_endpoint' <<<"$SECRET")
   unset SECRET
   ```
3. Take a snapshot now and wait for it (so the comparison is against a known moment).
   ```bash
   export SNAP=atpost-$ENV-valkey-drill-$(date -u +%Y%m%d%H%M)
   NODE=$(aws elasticache describe-replication-groups --replication-group-id $SRC --query 'ReplicationGroups[0].MemberClusters[0]' --output text)
   aws elasticache create-snapshot --cache-cluster-id "$NODE" --snapshot-name "$SNAP"
   date -u +%FT%TZ
   ```
   Repeat until it prints `available`:
   ```bash
   aws elasticache describe-snapshots --snapshot-name "$SNAP" --query 'Snapshots[0].SnapshotStatus' --output text
   ```
4. Straight after the `create-snapshot` command, count the keys on the SOURCE. The pod is deleted when the command ends.
   ```bash
   kubectl -n atpost run valkey-drill-$RANDOM --rm -i --restart=Never --quiet \
     --image=valkey/valkey:8-alpine --env="REDISCLI_AUTH=$REDISCLI_AUTH" -- \
     valkey-cli --tls -h "$SRC_HOST" -p 6379 --no-auth-warning INFO keyspace | tee valkey-keys-source.txt
   ```
   Expected: a line such as `db0:keys=1234,expires=1100,avg_ttl=…`.
5. Read the settings of the source and restore the snapshot into a new group. Start the clock.
   ```bash
   SUBNET_GROUP=$(aws elasticache describe-cache-clusters --cache-cluster-id "$NODE" --query 'CacheClusters[0].CacheSubnetGroupName' --output text)
   SG=$(aws elasticache describe-cache-clusters --cache-cluster-id "$NODE" --query 'CacheClusters[0].SecurityGroups[0].SecurityGroupId' --output text)
   PARAMS=$(aws elasticache describe-cache-clusters --cache-cluster-id "$NODE" --query 'CacheClusters[0].CacheParameterGroup.CacheParameterGroupName' --output text)
   TYPE=$(aws elasticache describe-cache-clusters --cache-cluster-id "$NODE" --query 'CacheClusters[0].CacheNodeType' --output text)
   KMS=$(aws kms describe-key --key-id alias/atpost-$ENV-elasticache --query 'KeyMetadata.Arn' --output text)
   date -u +%FT%TZ
   aws elasticache create-replication-group \
     --replication-group-id $DST \
     --replication-group-description "restore drill of $SRC" \
     --snapshot-name "$SNAP" \
     --engine valkey --cache-node-type "$TYPE" --num-cache-clusters 1 \
     --cache-subnet-group-name "$SUBNET_GROUP" --security-group-ids "$SG" \
     --cache-parameter-group-name "$PARAMS" \
     --transit-encryption-enabled --auth-token "$REDISCLI_AUTH" \
     --at-rest-encryption-enabled --kms-key-id "$KMS" \
     --tags Key=Purpose,Value=restore-drill > /dev/null
   aws elasticache wait replication-group-available --replication-group-id $DST
   date -u +%FT%TZ
   export DST_HOST=$(aws elasticache describe-replication-groups --replication-group-id $DST --query 'ReplicationGroups[0].NodeGroups[0].PrimaryEndpoint.Address' --output text)
   ```
   The second time printed minus the first is the time to restore (expect 10 to 20 minutes).
6. Count the keys on the RESTORED group.
   ```bash
   kubectl -n atpost run valkey-drill-$RANDOM --rm -i --restart=Never --quiet \
     --image=valkey/valkey:8-alpine --env="REDISCLI_AUTH=$REDISCLI_AUTH" -- \
     valkey-cli --tls -h "$DST_HOST" -p 6379 --no-auth-warning INFO keyspace | tee valkey-keys-restored.txt
   ```
7. Compare `keys=` in the two files. Expected: the restored number is equal to the source number or lower by the keys that expired in between (compare with `expires=`); never higher, never 0 when the source was not 0. Then spot-check one counter that does not expire:
   ```bash
   kubectl -n atpost run valkey-drill-$RANDOM --rm -i --restart=Never --quiet \
     --image=valkey/valkey:8-alpine --env="REDISCLI_AUTH=$REDISCLI_AUTH" -- \
     valkey-cli --tls -h "$DST_HOST" -p 6379 --no-auth-warning --scan --count 1000 | head -5
   ```
   Pick one key from the list, run `TYPE <key>` and the matching read (`GET`, `HGETALL`) against both hosts the same way, and write both answers in the record.
8. Delete the drill group and the drill snapshot.
   ```bash
   aws elasticache delete-replication-group --replication-group-id $DST --no-retain-primary-cluster
   aws elasticache wait replication-group-deleted --replication-group-id $DST
   aws elasticache delete-snapshot --snapshot-name "$SNAP" > /dev/null
   unset REDISCLI_AUTH
   ```
9. Fill in the drill record below and commit it.

## Part B. Real incident: put a restored group into service

Do this only with the lead present. Most Redis data rebuilds itself; restoring is the right call only when the counters or sessions matter more than the hours since the snapshot. The alternative is an empty group: skip step 1 and leave out `--snapshot-name` in step 2.

1. List the automatic snapshots and choose one.
   ```bash
   aws elasticache describe-snapshots --replication-group-id $SRC \
     --query 'Snapshots[].[NodeSnapshots[0].SnapshotCreateTime,SnapshotName]' --output text | sort
   export SNAP=<snapshot name>
   ```
2. Do Part A steps 1, 2 and 5 with `export DST=atpost-$ENV-valkey-restored`.
3. The endpoint of a replication group cannot be renamed. Every service reads `redis_addr` from its secret `atpost/<env>/<service>` in Secrets Manager. The lead updates `redis_addr` in each to `$DST_HOST:6379` (the AUTH token is unchanged), then forces the secrets to refresh and restarts the services:
   ```bash
   kubectl -n atpost annotate externalsecret --all force-sync=$(date +%s) --overwrite
   kubectl -n atpost rollout restart deploy
   ```
4. Check the edge answers, then sign in to the app and like a post.
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' https://api-qa.cleestudio.com/readyz
   ```
   Expected: `200`.
5. Tell the lead: Terraform's state still describes the old group. Do not run `terraform apply` on this environment until the state has been repaired.

## Drill record (copy this block for each drill)

```
ElastiCache restore drill
Date (UTC):                    
Environment:                   qa
Run by:                        
Snapshot name:                 
Snapshot taken (UTC):          
Restore started (UTC):         
Group available (UTC):         
Time to restore:               

                keys      expires
source
restored

Spot-checked key (name, type):       
Value on source / on restored:       
Drill group and snapshot deleted:    yes / no
Commands that had to be changed:     
Result:                              PASS / FAIL
Follow-ups:                          
```
