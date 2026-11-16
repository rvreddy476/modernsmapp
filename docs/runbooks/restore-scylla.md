# ScyllaDB: backup check and restore

**Status: UNTESTED.** Written 16 November 2026 from the configuration and the Scylla Manager documentation. No backup has run and no restore has been done in any environment. Every command here must be proven by the drill in Part C on QA before anyone relies on it. Fill in the drill record at the end.

What is backed up: every keyspace of the cluster `atpost-<env>` (`social_feed`, `social_engagement`, `social_notify`, `social_analytics`, `chatservice`), once a day at 20:30 UTC (02:00 IST), to the S3 bucket `terraform output -raw scylla_backups_bucket_name`.

Retention: Scylla Manager keeps the last 7 daily backups on QA and 14 on production and deletes older ones itself. The bucket keeps deleted files for 14 more days as old versions. There is no age-based deletion of live files (a backup re-uses files uploaded by earlier backups).

Where it is configured: `infra/terraform/modules/scylla/backup.tf` (Scylla Manager, the task), `infra/terraform/envs/<env>/scylla-backup.tf` (bucket, role).

Run every command in Git Bash. `ENV` is `qa` or `prod`. Do the drill on `qa` only.

## Part A. Once, after the Terraform apply that adds backups

1. Point kubectl at the cluster.
   ```bash
   export ENV=qa
   aws eks update-kubeconfig --name atpost-$ENV --region ap-south-1
   ```
2. Read the bucket name.
   ```bash
   cd infra/terraform/envs/$ENV
   export BUCKET=$(terraform output -raw scylla_backups_bucket_name)
   cd -
   echo "$BUCKET"
   ```
   Expected: `atpost-qa-scylla-backups-<8 hex characters>`.
3. Check Scylla Manager is running.
   ```bash
   kubectl -n scylla-manager get pods
   ```
   Expected: three pods `Running`: `scylla-manager-…`, `scylla-manager-controller-…`, and the manager's own database pod.
4. Check the Scylla pods' ServiceAccount carries the backup role.
   ```bash
   kubectl -n scylla get sa atpost-$ENV-member -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'; echo
   ```
   Expected: `arn:aws:iam::<account>:role/atpost-qa-scylla-backup-irsa`. If it prints nothing, run `terraform apply` once more (the ServiceAccount did not exist yet on the first apply) and repeat this step.
5. Restart the Scylla pods one at a time so they start with the role. The operator does the rolling restart.
   ```bash
   kubectl -n scylla patch scyllacluster atpost-$ENV --type merge \
     -p "{\"spec\":{\"forceRedeploymentReason\":\"backup role $(date -u +%F)\"}}"
   kubectl -n scylla get pods -w
   ```
   Wait until every `atpost-qa-…` pod is `Running` and ready again. Press Ctrl+C.
6. Check the agent in a Scylla pod can reach the bucket.
   ```bash
   export POD=$(kubectl -n scylla get pods -l scylla/cluster=atpost-$ENV -o name | head -1)
   kubectl -n scylla exec "$POD" -c scylla-manager-agent -- \
     scylla-manager-agent check-location --location "s3:$BUCKET"
   ```
   Expected: no output and exit code 0. An `AccessDenied` here means step 4 or 5 is not done.
7. Check the daily task exists.
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- sctool tasks -c scylla/atpost-$ENV
   ```
   Expected: a `backup/…` line with the cron `30 20 * * *` and a next activation time.
8. Run one backup now and wait for it.
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool backup -c scylla/atpost-$ENV -L "s3:$BUCKET" --retention 1
   ```
   It prints a task id such as `backup/3208ff15-…`. Watch it:
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool progress -c scylla/atpost-$ENV backup/<id printed above>
   ```
   Expected: `Status: DONE`.
9. List the backups in the bucket.
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool backup list -c scylla/atpost-$ENV -L "s3:$BUCKET"
   ```
   Expected: one snapshot tag such as `sm_20261116203000UTC` and the keyspaces under it.

## Part B. Every week: is the backup still running?

1. Run step 7 and step 9 of Part A.
2. Expected: the newest snapshot tag is from the last 24 hours and the task's last run is `DONE`.
3. If the newest tag is older than 48 hours, read the task's error:
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- sctool progress -c scylla/atpost-$ENV backup/<task id from step 7>
   ```

## Part C. Restore drill: one keyspace, QA only

The drill empties `social_engagement` on QA and brings it back from the backup. Likes, reactions, comment and share indexes and bookmarks on QA are unavailable for its duration (about 30 minutes). **Never run steps 4 and 5 on production.**

1. Set the variables (Part A steps 1 and 2), then:
   ```bash
   export POD=$(kubectl -n scylla get pods -l scylla/cluster=atpost-$ENV -o name | head -1)
   export TABLES="post_likes user_likes comment_likes post_shares user_shares user_bookmarks bookmark_check reactions_by_post comments_by_post post_counters engagement_counters"
   ```
2. Stop the service that writes the keyspace and note the time.
   ```bash
   kubectl -n atpost scale deploy/post-service --replicas=0
   date -u +%FT%TZ
   ```
3. Take a fresh backup and wait for `DONE` (Part A step 8). Then read its snapshot tag (Part A step 9) and keep it:
   ```bash
   export TAG=sm_<the newest tag from the list>
   ```
4. Count the rows of every table and keep the file.
   ```bash
   for t in $TABLES; do
     printf '%s\t' "$t"
     kubectl -n scylla exec "$POD" -c scylla -- cqlsh --request-timeout=600 \
       -e "SELECT COUNT(*) FROM social_engagement.$t;" | sed -n 4p | tr -d ' '
   done | tee scylla-counts-before.tsv
   ```
5. Empty the tables (this is the simulated loss).
   ```bash
   for t in $TABLES; do
     kubectl -n scylla exec "$POD" -c scylla -- cqlsh --request-timeout=600 \
       -e "TRUNCATE social_engagement.$t;"
   done
   ```
   Run step 4's loop again without `tee`: every count must be 0.
6. Restore the keyspace's data from the backup. Start the clock.
   ```bash
   date -u +%FT%TZ
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool restore -c scylla/atpost-$ENV -L "s3:$BUCKET" -T "$TAG" \
     -K 'social_engagement' --restore-tables
   ```
   It prints a task id `restore/…`. Watch it until `Status: DONE`:
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool progress -c scylla/atpost-$ENV restore/<id printed above>
   date -u +%FT%TZ
   ```
   If `sctool restore` refuses a flag, read `sctool restore --help` in the same pod, correct this runbook, and note it in the record.
7. Count again and compare.
   ```bash
   for t in $TABLES; do
     printf '%s\t' "$t"
     kubectl -n scylla exec "$POD" -c scylla -- cqlsh --request-timeout=600 \
       -e "SELECT COUNT(*) FROM social_engagement.$t;" | sed -n 4p | tr -d ' '
   done | tee scylla-counts-after.tsv
   diff scylla-counts-before.tsv scylla-counts-after.tsv && echo "ROW COUNTS MATCH"
   ```
   Expected: `ROW COUNTS MATCH`. Any differing line is a failed drill: keep both files and stop.
8. Start the writer again and check it is ready.
   ```bash
   kubectl -n atpost scale deploy/post-service --replicas=1
   kubectl -n atpost rollout status deploy/post-service --timeout=5m
   ```
9. Open the QA app, like a post, reload: the like is still there.
10. Fill in the drill record below and commit it.

## Part D. Real restore after losing the cluster

Use this when the Scylla cluster was rebuilt empty (all pods and volumes lost).

1. Set the variables (Part A steps 1 and 2).
2. Wait for the new cluster: `kubectl -n scylla get scyllacluster atpost-$ENV` shows all members ready. Do Part A steps 4 to 6.
3. Stop every writer.
   ```bash
   for d in post-service feed-service notification-service analytics-service chat-message-service; do
     kubectl -n atpost scale deploy/$d --replicas=0
   done
   ```
4. Create the keyspaces and tables from git (the schema is not taken from the backup).
   ```bash
   deploy/jobs/scylla-schema/apply.sh $ENV
   ```
5. List the backups and choose the newest tag from before the loss (Part A step 9). `export TAG=sm_<tag>`.
6. Restore every keyspace.
   ```bash
   kubectl -n scylla-manager exec deploy/scylla-manager -- \
     sctool restore -c scylla/atpost-$ENV -L "s3:$BUCKET" -T "$TAG" \
     -K 'social_feed,social_engagement,social_notify,social_analytics,chatservice' --restore-tables
   ```
   Watch with `sctool progress` until `DONE`.
7. Start the writers again:
   ```bash
   for d in post-service feed-service notification-service analytics-service chat-message-service; do
     kubectl -n atpost scale deploy/$d --replicas=1   # then sync in Argo CD for the real replica counts
   done
   ```
8. Everything written between the backup and the loss is gone (up to 24 hours). Home timelines can be rebuilt from Postgres with feed-service's `cmd/backfill`; likes, reactions and comments indexes written in that window cannot.
9. If the newest backup in the bucket was deleted by mistake, its files are kept as old S3 versions for 14 days; restoring those is manual (S3 console, "Show versions").

## Drill record (copy this block for each drill)

```
Scylla restore drill
Date (UTC):                 
Environment:                qa
Run by:                     
Scylla version / Manager version (sctool version): 
Backup snapshot tag:        
Writer stopped at (UTC):    
Restore started (UTC):      
Restore finished (UTC):     
Time to restore:            
Keyspace restored:          social_engagement

Table                  rows before   rows after   match
post_likes
user_likes
comment_likes
post_shares
user_shares
user_bookmarks
bookmark_check
reactions_by_post
comments_by_post
post_counters
engagement_counters

App check (like survives a reload):  pass / fail
Commands that had to be changed:     
Result:                              PASS / FAIL
Follow-ups:                          
```
