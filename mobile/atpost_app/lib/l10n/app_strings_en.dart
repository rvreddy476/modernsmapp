// English (en) Pulse strings.
//
// Legacy Map<String, String> approach (no ARB / flutter_localizations yet) —
// see lib/l10n/README.md for the rationale and migration plan.
//
// Keys are namespaced `pulse.<screen>.<element>`. Add new keys at the bottom
// of the relevant section so diffs stay readable.

const Map<String, String> appStringsEn = {
  // ---- Pulse onboarding: intent picker (B1) -------------------------------
  'pulse.intent.title': 'What are you looking for?',
  'pulse.intent.subtitle':
      'Pulse uses your intent to surface people who want the same kind of connection.',
  'pulse.intent.casual.title': 'Casual',
  'pulse.intent.casual.description':
      'Light dating, no pressure. Open to seeing where it goes.',
  'pulse.intent.casual.example':
      'e.g. coffee dates, weekend hangouts, getting to know people.',
  'pulse.intent.serious.title': 'Serious / Long-term',
  'pulse.intent.serious.description':
      'Looking for a real partner, not just a hangout buddy.',
  'pulse.intent.serious.example':
      'e.g. someone to build a steady relationship with.',
  'pulse.intent.marriage.title': 'Marriage',
  'pulse.intent.marriage.description':
      'Ready to commit. Looking for a life partner.',
  'pulse.intent.marriage.example':
      'e.g. shared values, family planning, long-term commitment.',
  'pulse.intent.footer':
      'You can change your intent anytime. Pulse will always show your current intent on your profile.',
  'pulse.intent.continue': 'Continue',
  'pulse.intent.error.save':
      'Could not save your intent. Please check your connection and try again.',

  // ---- Pulse onboarding: tune setup (B2) ----------------------------------
  'pulse.tune.title': 'Tune your Pulse',
  'pulse.tune.subtitle':
      'Move the dials a little. Pulse uses these to find people whose energy fits yours.',
  'pulse.tune.skip': 'Skip for now',
  'pulse.tune.save': 'Save and continue',
  'pulse.tune.error.save':
      'Could not save your Tune. Please check your connection and try again.',

  // Lifestyle rhythm
  'pulse.tune.lifestyle.label': 'Lifestyle rhythm',
  'pulse.tune.lifestyle.hint':
      'Are your weekends quiet and slow, or full and loud? Either is fine — be honest.',
  'pulse.tune.lifestyle.left': 'Quiet',
  'pulse.tune.lifestyle.right': 'Vibrant',

  // Conversation style
  'pulse.tune.conversation.label': 'Conversation style',
  'pulse.tune.conversation.hint':
      'Pick the one that feels most like you in a first conversation.',
  'pulse.tune.conversation.witty': 'Witty',
  'pulse.tune.conversation.deep': 'Deep',
  'pulse.tune.conversation.playful': 'Playful',
  'pulse.tune.conversation.direct': 'Direct',
  'pulse.tune.conversation.reflective': 'Reflective',

  // Relationship intent (mirrors B1 but locked to chosen value here)
  'pulse.tune.intent.label': 'Relationship intent',
  'pulse.tune.intent.hint':
      'This is what you picked earlier. Tap to change it.',

  // Faith & family weight
  'pulse.tune.faith.label': 'Faith and family',
  'pulse.tune.faith.hint':
      'How much do faith and family weigh in your day-to-day life?',
  'pulse.tune.faith.left': 'Light',
  'pulse.tune.faith.right': 'Central',

  // Region & language
  'pulse.tune.languages.label': 'Languages you speak',
  'pulse.tune.languages.hint':
      'Pick at least one. Pulse uses this to surface people you can actually talk to.',
  'pulse.tune.languages.english': 'English',
  'pulse.tune.languages.hindi': 'Hindi',
  'pulse.tune.languages.tamil': 'Tamil',
  'pulse.tune.languages.telugu': 'Telugu',
  'pulse.tune.languages.bengali': 'Bengali',
  'pulse.tune.languages.marathi': 'Marathi',

  // Marriage-only axes
  'pulse.tune.familyPlans.label': 'Family plans',
  'pulse.tune.familyPlans.hint':
      'How important is starting a family for you in the next few years?',
  'pulse.tune.familyPlans.left': 'Not a priority',
  'pulse.tune.familyPlans.right': 'Very important',
  'pulse.tune.education.label': 'Education importance',
  'pulse.tune.education.hint':
      'How much does a partner\'s education level matter to you?',
  'pulse.tune.education.left': 'Not much',
  'pulse.tune.education.right': 'A lot',

  // ---- Pulse onboarding: echoes consent (B3) ------------------------------
  'pulse.echoes.title': 'Bring your AtPost into Pulse?',
  'pulse.echoes.body':
      'Pulse can show your recent AtPost activity on your profile (your last 3 reels, top Q&A answers, communities you\'re active in). This makes Pulse signal-rich. You can hide individual items anytime.',
  'pulse.echoes.yes': 'Yes, surface my Echoes',
  'pulse.echoes.no': 'Not yet, only show what I add manually',
  'pulse.echoes.warning':
      'Without Echoes, your profile will feel sparser. Most people scroll past sparse profiles.',
  'pulse.echoes.error.save':
      'Could not save your choice. Please check your connection and try again.',

  // ---- Pulse: shared UI strings -------------------------------------------
  'pulse.app.name': 'Pulse',
  'pulse.cta.continue': 'Continue',
  'pulse.cta.back': 'Back',
  'pulse.cta.maybeLater': 'Maybe later',

  // ---- Sprint 3: snackbars & celebrations --------------------------------
  'pulse.spark.sent': 'Spark sent.',
  'pulse.spark.failed': 'Could not send Spark. Try again.',
  'pulse.match.celebration': 'It\'s a match!',
  'pulse.match.celebration.subtitle':
      'You both Sparked each other. Say hi while the energy is fresh.',

  // ---- Sprint 4: Aadhaar / DPDP disclosure -------------------------------
  'pulse.aadhaar.disclosure':
      'Pulse never stores or transmits your Aadhaar number. We only receive '
          'a one-time confirmation from DigiLocker that your name and date of '
          'birth match your government ID. You can revoke verification '
          'anytime from Safety Center.',

  // ---- Sprint 5: Premium tier --------------------------------------------
  'pulse.premium.title': 'Pulse Premium',
  'pulse.premium.tagline': 'More Pulse. More Sparks. More safety.',
  'pulse.premium.continue': 'Continue',
  'pulse.premium.disclosure':
      'Auto-renews on the day before expiry via UPI. You can cancel anytime; '
          'access continues till the end of the period. Pricing in INR including GST.',
  'pulse.premium.welcome.title': 'You\'re now a Premium member',
  'pulse.premium.welcome.subtitle': 'Here\'s what you unlocked.',
  'pulse.premium.feature.unlimitedSparks': 'Unlimited Sparks',
  'pulse.premium.feature.whoSparked': 'See who Sparked you',
  'pulse.premium.feature.stash25': '25 Stash slots',
  'pulse.premium.feature.incognito': 'Incognito browse',
  'pulse.premium.feature.boost': 'Pulse Boost — +5 daily, 1 per day',
  'pulse.premium.feature.matchExtend': 'Match-extend (+7 days)',
  'pulse.premium.feature.tuneFilters': 'Advanced Tune filters',
  'pulse.premium.feature.safeMeet': 'Safe-meet check-in',
  'pulse.premium.feature.priorityModeration': 'Priority moderation review',
  'pulse.premium.feature.readReceipts': 'Read receipts (per match)',
  'pulse.premium.checkout.failed': 'Checkout failed. Please try again.',
  'pulse.premium.checkout.retry': 'Retry',

  // Paywall copy.
  'pulse.paywall.incognito.title': 'Browse incognito',
  'pulse.paywall.whoSparked.title': 'See who Sparked you',
  'pulse.paywall.boost.title': 'Pulse Boost',
  'pulse.paywall.boost.buySingle': 'Buy single Boost (₹49)',
  'pulse.paywall.matchExtend.title': 'Extend this match',

  // Data export.
  'pulse.dataExport.title': 'Data export',
  'pulse.dataExport.cta': 'Request a new export',
  'pulse.dataExport.empty': 'No exports yet.',
};
