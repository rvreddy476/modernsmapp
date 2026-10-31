# Web (postbook-ui) fixes for Q&A — 30 Oct 2026

The website lives in another repo, so these fixes were not made here.
Server side: [audit-2026-10-30-qa-backend.md](audit-2026-10-30-qa-backend.md),
fixed on `codex/module-01-02-launch-safety`. Q&A stays closed at the gateway
(`QA_PUBLIC_ENABLED=false`) until these land.

## Must fix before opening Q&A

1. **Stored XSS (audit H3).** `questions/[id]/page.tsx` ~156-159 renders
   `answer.body_html || answer.body` with `dangerouslySetInnerHTML`.
   - Never pass `body` to `dangerouslySetInnerHTML`.
   - Render the server's `body_html` (now always server-built, escaped:
     only `<p>`, `<br>` and http/https links), and run DOMPurify on it as a
     second layer.
   - Stop building `` `<p>${body}</p>` `` on the client; comments are plain
     text and should render as text.
   - The server ignores any `body_html` the client sends.
2. **Search (M10).** The response is `data.questions` plus
   `data.pagination`, not an array at `data.data`. Handle 429
   `RATE_LIMITED` (signed-out callers get 30 searches a minute). At most 50
   per page, offset at most 500.
3. **Drafts (M10).** `POST /drafts/questions/:id`, `POST
   /drafts/answers/:id` and `GET /drafts/questions/:id` do not exist. Upsert
   by sending `id` in the body to `POST /drafts/questions` or
   `/drafts/answers`; lists come back as `{drafts, pagination}`; there is no
   single-draft GET. Snake_case fields are accepted.
4. **Local feed (M10).** Requires `lat` and `lng`, and nothing writes
   `local_scopes` yet, so hide it until the write side exists.

## Server changes the web will see

| Case | Now |
|---|---|
| Moderation | `/v1/qa/admin/*` answers 410 `QA_MODERATION_MOVED`; moderators use the admin console (`/v1/admin/qa/*`). Retire the old web admin pages. |
| Create topic | `POST /v1/qa/topics` answers 410 `QA_TOPIC_CREATE_MOVED` (console only, `qa:topics.manage`) |
| Close a question you don't own | 403; closing a non-open question 409 |
| Accept an answer from another question | 400 |
| Vote on your own question, answer or comment | 403 |
| Repeat answer request | 409 |
| Posting too fast | 429 (questions 10/h, answers 30/h, comments 60/h, …) |
| Over-long text | 400 (title 300, question 20k, answer 30k, comment 2k) |
| Anything on a question you can't see (private community, pending approval, deleted) | 403 or 404 |
| Editing a question | the slug no longer changes; old slugs still resolve |
| Search | whole-word prefix matching, ordered by votes then date |
| Community counts | may lag by up to 30 s |
| Answer requests | `target_user_id` and `response: accept/decline` are accepted |

## Admin console: seller KYC documents can be PDFs (31 Oct 2026)

`GET` on the seller KYC document view (admin console → commerce) may now
answer `application/pdf` as well as image/jpeg, png or webp, with
`Content-Disposition: attachment` and a sandboxing CSP. When the type is
PDF, save the blob as a download or render it in the page with pdf.js on a
canvas; do not navigate to it or put it in an iframe. Images are unchanged.

## Q&A reports are de-duplicated (31 Oct 2026)

`POST /v1/qa/reports`: 201 for a new report; 200 with the reporter's
existing open report when they report the same target again (original
reason kept — show "You already reported this"); 404 `NOT_FOUND` when the
target is missing or not visible to the reporter; 400 `INVALID_REQUEST`
for reporting your own post or yourself. After a moderator closes a report
the same user can report the target again (201).
