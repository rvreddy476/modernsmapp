# Dead-letter replay: list a `.dlq` topic and send messages back

**Status: UNTESTED.** Written 18 November 2026 (condition 6, reels-5). The command and its rules are unit-tested (`internal/dlqreplay`, `cmd/dlq-replay`); it has never been run against a real broker. Do the dry run on QA first and fill in the record at the end.

What it is: `/dlq-replay` in the post-service image (`Architecture/services/post-service/cmd/dlq-replay`). It reads every message on one dead-letter topic, prints them, and, only with `-write`, re-publishes the ones you choose to the topic they came from.

Dead-letter topics in use:

| Dead-letter topic | Written by | Alert |
|---|---|---|
| `media.events.dlq` | post-service, group `post-service-media-transcode` (a transcode result that could not reach its post) | `MediaEventsDeadLettered` (media-service rules) |
| `social.events.v1.dlq` and the other `<topic>.dlq` of post-service's consumers (live VOD, entitlements, story moderation, action-link and comment report holds) | post-service | `PostEventsDeadLettered` |
| `<call topic>.dlq` | notification-service's call consumer (malformed call events) | `NotificationEventsDeadLettered` |

Rules the command keeps, so you do not have to:

- Nothing is written without `-write`. Without `-all` or `-offsets` it only lists.
- In production (`ENV=prod` in the pod) it refuses unless `-confirm-production` repeats the dead-letter topic's name.
- It only writes to the topic the dead-letter topic belongs to (`media.events.dlq` goes back to `media.events`, nothing else).
- It never sends a message twice: after each re-publish it writes a marker to the dead-letter topic, and the next run shows that message as `replayed` and skips it.
- The consumer that dead-lettered a message also wrote its "already processed" receipt in Redis, so a plain re-publish would be dropped. The command deletes that ONE group's receipt just before it re-publishes. Every other consumer group on the topic still has its receipt and drops the repeat.

Run in Git Bash. `ENV` is `qa` or `prod`.

## Replay a dead letter

1. Fix the cause first. Read the error the consumer recorded (step 3 prints it). A message replayed before the fix is dead-lettered again.
2. Connect to the cluster and pick a post-service pod.
   ```bash
   export ENV=qa
   aws eks update-kubeconfig --name atpost-$ENV --region ap-south-1
   export POD=$(kubectl -n atpost get pods -l app.kubernetes.io/name=post-service,app.kubernetes.io/component!=worker -o name | head -1)
   ```
3. List the dead-letter topic. Nothing is written.
   ```bash
   kubectl -n atpost exec "$POD" -- /dlq-replay -dlq media.events.dlq
   ```
   Expected: `media.events.dlq: N dead-lettered message(s), source topic media.events`, then one line per message: `partition/offset`, `pending` or `replayed`, time, `group=`, `type=`, `event=` and `error=`.
   In prod add `-confirm-production media.events.dlq` to this and every later step.
4. Dry run the replay. Choose the messages by `partition/offset` from step 3, or all pending ones of one group.
   ```bash
   kubectl -n atpost exec "$POD" -- /dlq-replay -dlq media.events.dlq -offsets 0/12,1/7
   kubectl -n atpost exec "$POD" -- /dlq-replay -dlq media.events.dlq -all -group post-service-media-transcode
   ```
   Expected: `would replay 0/12 -> media.events` per message and `dry run: nothing written`. A `refused` line means the message records another source topic; it is never sent. `skip ... already replayed` means a marker exists.
5. Replay for real: the same command with `-write`.
   ```bash
   kubectl -n atpost exec "$POD" -- /dlq-replay -dlq media.events.dlq -offsets 0/12,1/7 -write
   ```
   Expected: `replayed 0/12 -> media.events` per message, exit status 0.
6. Check it worked.
   ```bash
   kubectl -n atpost exec "$POD" -- /dlq-replay -dlq media.events.dlq
   kubectl -n atpost logs "$POD" --since=10m | grep -iE 'dlq|media' | tail -20
   ```
   Expected: the replayed messages now show `replayed`, and no new `message sent to DLQ` line for them. For `media.events`: the post leaves `processing` (and `PostVideoPendingTooLong` resolves).
7. If step 5 stopped with an error, run it again unchanged. Messages already sent have their marker and are skipped. The one exception is `re-published, but the replay marker failed`: that message went out but has no marker, so the next run sends it once more; the consumers drop the repeat by event id.

Exit status: 0 done, 1 a Kafka or Redis call failed (a re-run is safe), 2 refused or wrong flags.

Do not replay a call notification (`<call topic>.dlq`): an "incoming call" push minutes or hours late is worse than none. List it for the record only.

## Record

| Date | Env | Topic | Messages | Result | By |
|---|---|---|---|---|---|
| | | | | | |
