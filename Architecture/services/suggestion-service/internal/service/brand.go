package service

// brandName is the product name as it appears to a user reading a suggestion.
//
// It is a compile-time constant on purpose, and the choice is worth recording
// because two other shapes were considered:
//
//   - Config/env value. Rejected. The brand is not environment-specific — dev,
//     staging and prod all render the same word — so a knob buys nothing but a
//     new failure mode: an unset variable ships an empty or stale name to real
//     users, and it would be a deploy-time surprise rather than a compile-time
//     one. A rename is a code change anyway; the web and Android copies of this
//     wording live in source too.
//
//   - Drop the prose and let the client render from reason_codes. This is the
//     right long-term shape — the codes ("POPULAR", "NEW_CREATOR", …) are
//     already on the wire, so each client could own, localise and never drift
//     its own copy. It is not done here because explain_text is rendered
//     verbatim today by the shipped Android app
//     (feature/chat/.../SuggestionsTab.kt: person.explainText.ifBlank { … })
//     and by the web right rail (apps/social/src/chrome/RightRail.tsx), and
//     both fall back to generic copy only when the field is blank. Emptying it
//     server-side would silently downgrade every installed client to
//     "Suggested for you". That migration needs the clients to ship a full
//     code→copy map first, and the server to keep sending prose until they do.
//
// Until then: one constant, so the next rename is one grep.
const brandName = "Momentum"
