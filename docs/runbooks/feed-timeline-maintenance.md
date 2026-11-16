# Feed timeline maintenance, and turning off `FEED_INDEX_LEGACY_READ`

**Status: never run in any environment** (finding feed-48, 16 November 2026). `FEED_INDEX_LEGACY_READ` is `"true"` in every values file and stays `"true"` until the check in Part C passes for that environment.

What the job is: `cmd/timeline-maintenance` in feed-service, shipped inside the feed-service image as `/timeline-maintenance`, run as a one-off Kubernetes Job by `deploy/jobs/timeline-maintenance/run.sh`. In one pass over the Scylla keyspace `social_feed` it:

- collapses duplicate rows of a post onto one key,
- deletes rows of posts older than 360 days and rows with no post id,
- gives rows written before the TTL existed the time they have left,
- writes an entry in the new index `timeline_index_by_post_v2` for every row it keeps.

What the flag is: while `FEED_INDEX_LEGACY_READ` is not `"false"`, deleting or editing a post looks up its timeline rows in the new index AND in the old one (`timeline_index_by_post`). feed-service already writes only the new index. Rows written before that are found only through the old index until the job has indexed them. Turn the flag off before that and deleted posts written earlier stay in people's feeds.

Safety built into the job (read from `cmd/timeline-maintenance/main.go`):

- Without `--apply` it is a **dry run**: it reads everything, writes nothing, and prints what it would do.
- It is safe to run twice and safe to stop. A stopped run prints `STOPPED. Re-run with -tables <t> -start <n> to resume.`
- It reads and writes at QUORUM with 4 workers; it runs beside live traffic.
- The last step (`-truncate-legacy-index`) empties the old index. It cannot be undone except from a Scylla backup. It does nothing without `--apply`.
- `run.sh` accepts only `qa` or `prod` and replaces any earlier `timeline-maintenance` Job.

Run in Git Bash from the repository root.

## Part A. Before you start

1. Set the variables and point kubectl at the cluster.
   ```bash
   export ENV=qa                 # qa or prod
   aws eks update-kubeconfig --name atpost-$ENV --region ap-south-1
   ```
2. Check the Scylla schema is applied (the new index table must exist).
   ```bash
   POD=$(kubectl -n scylla get pods -l scylla/cluster=atpost-$ENV -o name | head -1)
   kubectl -n scylla exec "$POD" -c scylla -- cqlsh -e "DESCRIBE TABLE social_feed.timeline_index_by_post_v2;" | head -3
   ```
   Expected: a `CREATE TABLE social_feed.timeline_index_by_post_v2` line. If it says the table does not exist: `deploy/jobs/scylla-schema/apply.sh $ENV`, then repeat.
3. Check feed-service is deployed and read the image it runs. The job must use the same image.
   ```bash
   export IMAGE=$(kubectl -n atpost get deploy feed-service -o jsonpath='{.spec.template.spec.containers[0].image}')
   echo "$IMAGE"
   ```
   Expected: `<account>.dkr.ecr.ap-south-1.amazonaws.com/atpost/feed-service:<sha>`.
4. Production only: check last night's Scylla backup exists (`docs/runbooks/restore-scylla.md`, Part B). Do not continue on production without one.

## Part B. Run it

1. Dry run. Nothing is written.
   ```bash
   deploy/jobs/timeline-maintenance/run.sh $ENV "$IMAGE" 2>&1 | tee timeline-dryrun-$ENV.log
   ```
   The log starts with `timeline maintenance: DRY RUN (nothing is written; add -apply), tables=home,author ranges=4096 workers=4 start=0` and ends with one line per table:
   ```
   home: <rows> rows read, delete/duplicate=…, delete/expired=…, delete/ghost=…, delete/rekey=…, index/index=…, write/duplicate=…, write/rekey=…, write/ttl=…
   author: <rows> rows read, …
   ```
   A count that is 0 is not printed.
2. Read the two lines and write the numbers in the record at the end. Stop and ask the lead if:
   - the log ends with `STOPPED` or the Job failed (`kubectl -n scylla get job timeline-maintenance`), or
   - `delete/expired` plus `delete/ghost` is more than half of `rows read` (it would delete most of the feed: the clock or the data is wrong).
   On an environment that has never had users (a new production cluster) both lines say `0 rows read`: go straight to Part C.
3. Apply.
   ```bash
   deploy/jobs/timeline-maintenance/run.sh $ENV "$IMAGE" --apply 2>&1 | tee timeline-apply-$ENV.log
   ```
   Expected: the first line says `APPLY`, and the two last lines carry about the same counts as the dry run (traffic in between moves them a little).
4. If the run stops (`<table>: STOPPED. Re-run with -tables <table> -start <n> to resume.`), resume that table from the range it printed, then run any table that had not started:
   ```bash
   deploy/jobs/timeline-maintenance/run.sh $ENV "$IMAGE" --apply -- -tables <table> -start <n>
   ```
5. Run the dry run again (step 1, with `tee timeline-verify-$ENV.log`). This is the proof the pass is complete.

## Part C. The check that says the flag can be turned off

All four must be true for the environment. Write each result in the record.

1. The apply run finished: `timeline-apply-$ENV.log` has a final line for `home` and for `author`, and no `STOPPED` (or every stopped table was resumed to its end).
   ```bash
   grep -E '^.*(home|author): [0-9]+ rows read' timeline-apply-$ENV.log; grep -c STOPPED timeline-apply-$ENV.log
   ```
2. The verify dry run after it finds nothing left to repair: its `home:` and `author:` lines carry no `…/duplicate`, no `…/rekey` and no `write/ttl`. If it is not clean, run Part B step 3 and step 5 once more; if it is still not clean, something is writing rows the old way: stop and tell the lead. (`index/index` is printed for every kept row on every run, because the job always re-plans the index entry: it must equal `rows read` minus the deletes. A few `delete/expired` are normal: posts that turned 360 days old since the apply.)
   ```bash
   grep -E '(home|author): [0-9]+ rows read' timeline-verify-$ENV.log
   grep -E "/duplicate=|/rekey=|write/ttl=" timeline-verify-$ENV.log && echo "NOT CLEAN" || echo "CLEAN"
   ```
   Expected: `CLEAN`.
3. The feed-service image that is live writes only the new index. True for every image built from this branch since the index change (F-24/F-28); confirm the live image is the one from Part A step 3 and not an older one.
4. A delete reaches feeds through the new index alone. On QA: sign in as a test account that has a follower, create a post, wait until it shows in the follower's home feed, delete it, reload the follower's feed. Expected: the post is gone within a few seconds. Then do the same for one post created BEFORE the apply run (an old post of the test account). Expected: gone as well.

If any of the four fails, leave the flag `"true"`.

## Part D. Turn the flag off (lead, after Part C passes for that environment)

1. In `deploy/services/feed-service/values-<env>.yaml` set `FEED_INDEX_LEGACY_READ: "false"`. The chart's QA guard requires QA and production to carry the same value, so change both files in one commit, and only once Part C has passed on both (a production cluster that never had users passes with `0 rows read`).
2. Deploy feed-service the normal way (promote, sync in Argo CD).
3. Check the pods run with the new value.
   ```bash
   kubectl -n atpost get deploy feed-service -o jsonpath='{range .spec.template.spec.containers[0].env[?(@.name=="FEED_INDEX_LEGACY_READ")]}{.value}{"\n"}{end}'
   ```
   Expected: `false`.
4. Repeat Part C step 4 (delete an old post, it leaves the follower's feed).
5. Keep it like this for at least 7 days before the next part. To go back, set the flag to `"true"` and deploy: nothing has been deleted yet.

## Part E. Empty the old index (lead; cannot be undone)

1. Check the flag is `false` on the running pods (Part D step 3) and that last night's Scylla backup exists.
2. Dry run of the truncate, then the truncate.
   ```bash
   deploy/jobs/timeline-maintenance/run.sh $ENV "$IMAGE" -- -truncate-legacy-index
   deploy/jobs/timeline-maintenance/run.sh $ENV "$IMAGE" --apply -- -truncate-legacy-index
   ```
   Expected: `dry run: would TRUNCATE social_feed.timeline_index_by_post (add -apply)`, then `truncated social_feed.timeline_index_by_post`.

## Not covered here

- Echo's `notes_timeline` has its own modes (`-tables notes`, `-purge-notes-author`, `-rebuild-notes-user`); they do not affect this flag.
- Dev (docker compose on the dev box) is not reachable by `run.sh`. There the binary runs inside the feed-service container: `docker compose exec feed-service /timeline-maintenance` (dry run), same flags. The lead runs it.

## Run record (copy this block for each environment)

```
Feed timeline maintenance
Date (UTC):                    
Environment:                   
Run by:                        
feed-service image (sha):      
Scylla backup tag before run:  

                 rows read  delete/duplicate  delete/rekey  delete/expired  delete/ghost  write/duplicate  write/rekey  write/ttl  index/index
dry run   home
dry run   author
apply     home
apply     author
verify    home
verify    author

Stopped and resumed:           no / table + range
Part C 1 (apply finished):     pass / fail
Part C 2 (verify is CLEAN):    pass / fail
Part C 3 (live image):         pass / fail
Part C 4 (delete, new post):   pass / fail
Part C 4 (delete, old post):   pass / fail
Flag may be turned off:        YES / NO
Flag turned off on (date):     
Old index truncated on (date): 
```
