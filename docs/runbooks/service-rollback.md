# Roll one service back to the previous image

**Status: UNTESTED.** Written 16 November 2026. No rollback has been done in any environment. Drill it once on QA (deploy N, roll back to N-1) and fill in the record at the end.

Facts this page relies on:

- A service's image tag is the commit sha in `deploy/services/<service>/values-<env>.yaml`, on branch `qa` (QA) or `release/prod` (production). Git is the source of truth; Argo CD deploys what the branch says, on a manual sync.
- Argo CD applications are named `qa-<service>` and `prod-<service>`; pods run in namespace `atpost`.
- **Migrations are forward-only and run when the first pod of image N starts.** Rolling the image back does NOT undo them. Image N-1 then runs on N's schema. Step 3 checks whether it can.

Run in Git Bash from the repository root. "N" is the image that is live and bad; "N-1" is the one before it.

## Steps

1. Set the variables.
   ```bash
   export ENV=qa                 # qa or prod
   export SVC=feed-service       # the deploy name: the folder under deploy/services
   export BRANCH=qa              # qa for QA, release/prod for production
   export APP=$ENV-$SVC
   export VALUES=deploy/services/$SVC/values-$ENV.yaml
   aws eks update-kubeconfig --name atpost-$ENV --region ap-south-1
   ```
2. Read N and N-1 from git. Both must be 40-character shas and different.
   ```bash
   git fetch origin $BRANCH
   export N=$(git show origin/$BRANCH:$VALUES > /tmp/values-n.yaml && bash scripts/ci/values-tag.sh get /tmp/values-n.yaml image.tag)
   for c in $(git log --format=%H origin/$BRANCH -- $VALUES); do
     git show $c:$VALUES > /tmp/values-prev.yaml
     PREV=$(bash scripts/ci/values-tag.sh get /tmp/values-prev.yaml image.tag)
     [ "$PREV" != "$N" ] && break
   done
   export PREV; echo "N=$N"; echo "N-1=$PREV"
   kubectl -n atpost get deploy $SVC -o jsonpath='{.spec.template.spec.containers[0].image}'; echo
   ```
   Expected: the last line ends in `:$N`. If it does not, the cluster is not running what the branch says: sync first, or stop and ask the lead.
3. Check whether N's migrations work with N-1. `SRC` is the service's source folder: `Architecture/services/<service>`, except `identity-<x>-service` is `identity-platform/services/<x>-service` and `chat-<x>` is `chat-service/services/<x>` (`chat-call-service` is `call-service`, `chat-message-service` is `message-service`, `chat-ws-gateway` is `ws-gateway`).
   ```bash
   export SRC=Architecture/services/$SVC
   bash scripts/verify/migration-compat.sh $PREV $N -- $SRC; echo "exit=$?"
   git diff $PREV $N -- $SRC ':(exclude)*.sql' ':(exclude)*.cql' ':(exclude)*_test.go' \
     | grep -nE '^\+.*(ALTER TABLE|DROP (TABLE|COLUMN)|RENAME (TO|COLUMN)|CREATE TABLE)'
   ```
   Read the result:
   - `exit=0` and the second command prints nothing: **compatible**. Go to step 4.
   - `exit=1`: a migration removed, renamed or tightened something. Read each flagged line. If N-1's code does not use the object at all, it is compatible (say why in the record); otherwise **do not roll back**: go to "When N-1 cannot run on N's schema".
   - The second command prints lines: the service changes schema in Go at boot (post-service `ensureSchema`). Read them the same way.
4. Set the tag back to N-1 on the environment branch, through a pull request.
   ```bash
   git switch -c rollback/$ENV-$SVC-$(date -u +%Y%m%d-%H%M) origin/$BRANCH
   bash scripts/ci/bump-service-tags.sh $SVC $VALUES $PREV
   git diff --stat            # expected: 1 file changed (the tag line, and the worker tag if the service has a worker)
   git commit -am "rollback($ENV): $SVC to ${PREV:0:12}"
   git push -u origin HEAD
   gh pr create --base $BRANCH --title "rollback($ENV): $SVC to ${PREV:0:12}" --body "Rolling back from ${N:0:12}. Reason: <one line>. Migration check: <compatible / why>."
   gh pr merge --squash
   ```
5. Sync the application in Argo CD. In a second terminal, keep this running:
   ```bash
   kubectl -n argocd port-forward svc/argocd-server 8080:80
   ```
   In the first terminal (the password prompt wants the value of the secret `atpost/<env>/argocd/admin`; type it at the prompt, never on the command line):
   ```bash
   argocd login localhost:8080 --username admin --plaintext
   argocd app get $APP --refresh > /dev/null
   argocd app diff $APP       # expected: only the image line(s) change, to :$PREV
   argocd app sync $APP
   argocd app wait $APP --health --timeout 600
   ```
6. Check the rollout.
   ```bash
   kubectl -n atpost rollout status deploy/$SVC --timeout=5m
   kubectl -n atpost get deploy $SVC -o jsonpath='{.spec.template.spec.containers[0].image}'; echo
   kubectl -n atpost get pods -l app.kubernetes.io/name=$SVC
   ```
   Expected: `successfully rolled out`, the image ends in `:$PREV`, every pod `Running` and ready with 0 restarts.
7. Check readiness and the logs.
   ```bash
   PORT=$(awk '/^service:/{p=1} p&&/^  port:/{print $2; exit}' $VALUES | tr -d '\r')
   kubectl -n atpost port-forward deploy/$SVC 18080:$PORT > /dev/null &
   sleep 3; curl -s -o /dev/null -w '%{http_code}\n' localhost:18080/readyz; kill %1
   kubectl -n atpost logs deploy/$SVC --since=5m | grep -iE 'error|panic|column .* does not exist|relation .* does not exist' | head -20
   ```
   Expected: `200` and no schema errors. A "column does not exist" line means step 3 was read wrongly: go to "When N-1 cannot run on N's schema".
8. Check the edge and use the feature that was broken.
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' https://api-qa.cleestudio.com/readyz     # api.cleestudio.com on production
   ```
   Expected: `200`. (`scripts/smoke/dev-smoke.sh` refuses non-dev hosts; there is no scripted smoke for QA or production yet.)
9. Write down what happened: fill in the record below (for a drill) or the incident note (for a real rollback).

## If git or GitHub is unavailable (emergency only)

1. Do steps 1 to 3 and 5 up to `argocd login`.
2. List what Argo CD deployed before and roll back to the entry that carries N-1.
   ```bash
   argocd app history $APP
   argocd app rollback $APP <ID of the previous entry>
   ```
3. Do steps 6 to 8.
4. As soon as git is back, do step 4. Until the branch says N-1, the next sync deploys N again. Unproven: these applications have two sources (chart and values), and `argocd app rollback` on such an application depends on the Argo CD version installed.

## When N-1 cannot run on N's schema

1. Do not roll back the image. Tell the lead.
2. Fix forward: build image N+1 with the bug fixed and promote it the normal way. This is the default.
3. If N is unusable and the fix is not quick: N+1 is N-1's code plus one NEW migration that gives N-1 what it needs back (add the dropped column again as nullable, drop the new NOT NULL or constraint, add the old name back beside the new one). Never edit or delete an applied migration file, and never edit `schema_migrations` by hand.
4. If the migration destroyed data (DROP COLUMN, DROP TABLE, TRUNCATE): restore a copy of the database to just before N's first pod started (`docs/runbooks/restore-aurora.md`, Part A steps 1 to 7 with that time as T0) and copy the lost column or table back from it. The lead decides and runs this.

## The rule for every migration

**A migration must work with the previous image.** Expand first, contract in a later release:

| To do this | Release N (expand) | A later release (contract) |
|---|---|---|
| Add a column | Add it nullable, or NOT NULL with a DEFAULT | nothing |
| Make a column required | Add it nullable; new code writes it; backfill | `SET NOT NULL`, once no image that omits it can be rolled back to |
| Rename a column or table | Add the new one; new code writes both and reads the new one; backfill | Drop the old one |
| Change a type | Add a new column of the new type; write both; backfill | Drop the old column |
| Remove a column or table | Stop reading and writing it in code | Drop it |
| Add a CHECK, UNIQUE or foreign key | Make the code obey it; clean the data | Add the constraint |

- The contract migration ships at least one release after the code stopped using the old shape, so that N-1 is always a safe rollback target.
- A contract statement carries a comment inside it saying why it is safe: `-- compat: ok unused since release <date> (<what stopped using it>)`. `scripts/verify/migration-compat.sh` flags every removing, renaming or tightening statement that lacks one.
- Before every promotion, run the check between the tag that is live and the tag being promoted:
  ```bash
  bash scripts/verify/migration-compat.sh <tag in values-<env>.yaml> <tag being promoted>
  ```
  The check is not in CI yet; run it by hand. `bash scripts/verify/migration-compat.sh --self-test` proves the check itself.
- The same rule holds for Scylla (`.cql`) and for schema created in Go at boot.

## Drill record (copy this block for each drill)

```
Service rollback drill
Date (UTC):                       
Environment:                      qa
Run by:                           
Service:                          
N (sha):                          
N-1 (sha):                        
Migration check (step 3):         exit 0 / exit 1 + reason it is compatible
Rollback PR:                      
Sync started (UTC):               
Rollout complete (UTC):           
Time from decision to healthy:    
/readyz after rollback:           
Schema errors in the log:         none / <lines>
Feature check:                    pass / fail
Commands that had to be changed:  
Result:                           PASS / FAIL
Follow-ups:                       
```
