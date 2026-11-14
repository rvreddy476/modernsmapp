# Echo look v3: serif conversation cards

Approved by the founder on 14 Nov 2026. It replaces the X-style look (left avatar
column, bare grey icon row, "What is happening?!"). **Only the presentation changes.**
Every feature, route, API call, state, permission rule and test contract stays as it
is: Premium gate, threads, quotes, replies as notes, polls, GIF, photos, scheduling,
bookmarks, views, hashtags, For you / Following, profiles, notifications, search.

Echo must look like neither X nor a Home feed post (a photo-led card with a
full-width Like / Comment / Share bar).

## Design language (Momentum)

- **Note text** is set in the Momentum serif, **Bodoni Moda**, larger than body text:
  timeline 18 sp/px (16 for a long note over ~280 chars), note page 22, composer 20.
  Line height ~1.4. Everything else (names, buttons, counts) stays in the normal UI
  fonts (Figtree for body, Outfit for headings).
- **Author on top** (changed 15 Nov 2026 at the founder's request): small avatar +
  display name + verified mark, then how long ago it was posted (`Asha Rao · 2h`,
  `· Edited`) in secondary text. On a card the topic (`#firsthashtag`, `Thread`,
  `Quote`, `Reply to @name`) sits at the right end of that row in the **ember accent**,
  small (12). No @handle on cards. The note page shows the name with @handle and the
  Follow pill on top, then the meta line (`#tag · 2:14 pm · 1.2k views`), then the text.
  Quoted notes put their author and age above their text too.
  **No left avatar column anywhere.**
- **Card**: rounded 16, surface colour, hairline border, padding 14. Cards are separate
  with a 10 gap, not full-bleed rows with dividers.
- **Replies inside the card**: on the timeline, up to **2** top replies as chat bubbles
  (surface-1 fill, radius 12, bold name + text, one line each, ellipsised), then
  "N replies" in secondary text which opens the note page. No replies → no bubble area.
  Where the client does not already have reply data for a timeline note, fetch the first
  replies for visible cards lazily (reuse the existing replies call, limit 2, cached per
  note); never block the timeline on it, and show nothing until they arrive.
- **Actions** are labelled pills (radius full, hairline border, 12): **Reply**, **Echo it**
  (the existing repost/quote menu), **heart + count**, and a **More** (…) menu with the
  existing items (bookmark/Save, share, copy link, report, mute, delete, pin…). No bare
  icon row.
- Media (photos, GIF), polls and quoted notes sit inside the same card between the text
  and the signature, with radius 12. A quoted note renders as a smaller nested serif card.

## Screens

1. **Timeline** (Echo home): header "Echo" in the serif; For you / Following as two pill
   tabs (selected = accent tint); cards as above; a centred **Write a note** pill button
   (feather icon, ember) at the bottom of the screen on Android (above the bottom bar) and
   at the top of the column on the web, replacing the X-style inline composer / floating
   feather. Threads show as one card with "Thread · N parts" and a "Read the thread" link.
2. **Note page**: back; the note in a larger card with an **accent border**; its meta
   line `#tag · 2:14 pm · 1.2k views`; signature with a Follow pill; counts as labelled
   pills (`42 hearts`, `3 echoes`, `Save`); then a "Conversation · N" label and the
   replies as bubbles with a small avatar beside each, and a "Heart · Reply · 1h" line
   under each bubble. A reply to a reply is indented one step (max 2 steps, then flat
   with "Replying to @x"). Thread parts by the same author show as consecutive serif
   cards joined by a thin accent line. A rounded reply box at the bottom:
   "Reply to <name>…".
3. **Composer** (sheet on Android, modal + /echo/compose on web): Cancel, and a filled
   accent **Post note** pill; the text area IS a serif card (author signature on top
   with "Everyone can reply" — the existing reply-policy control); placeholder
   **"What's worth saying today?"**; labelled pills **Photo**, **GIF**, **Poll**,
   **Later** (schedule); **Add to thread**; "N left" counter. Thread parts stack as
   cards. Reply mode: the parent note shown above as a small card, placeholder
   "Your reply…", button "Reply".
4. **Web wide layout**: left menu (Echo wordmark in serif, Home, Explore, Notifications,
   Saved, Scheduled, Profile, Write a note), centre column of cards, right column
   "Talked about now" (trends) and who-to-follow if it exists. Same cards as the phone.
5. **Other pages** (profile, explore/search, hashtag, notifications, bookmarks,
   scheduled, quotes): reuse the new card; profile header keeps its data but drops the
   X layout cues (no banner-over-avatar X crop); tabs as pills.

## Words

- "Repost" stays as the action inside the "Echo it" menu: **Echo it** opens a menu with
  **Re-echo** (repost) and **Quote**. Counts read "3 echoes".
- "Post note", "Write a note", "What's worth saying today?", "Reply to <name>…",
  "Conversation", "Read the thread", "Talked about now", "Save".

## Rules for the build

- Colours and fonts only from the app's single token place (web: postbook-ui's tokens /
  Tailwind theme; Android: `core/designsystem` theme). Add a token there if one is missing
  (e.g. an `echoSerif` font family) instead of hard-coding.
- Keep every existing test id / content description that tests rely on; update tests
  whose expectations are about the old look; add tests for the reply bubbles (max 2,
  "N replies", hidden when none) and the topic line rules.
- Light and dark themes both.
