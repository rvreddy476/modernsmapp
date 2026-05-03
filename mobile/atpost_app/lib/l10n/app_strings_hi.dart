// Hindi (hi) Pulse strings.
//
// Translator instructions: replace each value in place; do not change keys.
// Strings marked `[NEEDS TRANSLATION]` still fall back to the English string
// at runtime (the resolver in `app_strings.dart` will use English when a key
// is absent from this map). When a translation is added, drop the marker
// and put the localised text as the map value.
//
// Sprint 5: 5 high-priority Pulse strings translated by the in-house team
// for the Sprint-5 demo. The full sweep ships in Sprint 6.

import 'package:atpost_app/l10n/app_strings_en.dart';

const Map<String, String> _hiOverrides = {
  // Onboarding intent picker title.
  'pulse.intent.title': 'आप क्या ढूँढ़ रहे हैं?',
  // Tune setup primary CTA.
  'pulse.tune.save': 'सहेजें और आगे बढ़ें',
  // "Spark sent" snackbar.
  'pulse.spark.sent': 'Spark भेजा गया।',
  // "It's a match!" celebration.
  'pulse.match.celebration': 'मैच हो गया!',
  // Aadhaar disclosure copy.
  'pulse.aadhaar.disclosure':
      'Pulse आपका आधार नंबर कभी नहीं रखता या भेजता है। हमें DigiLocker से '
          'सिर्फ़ एक पुष्टि मिलती है कि आपका नाम और जन्म तिथि आपके सरकारी आईडी '
          'से मेल खाते हैं। आप कभी भी Safety Center से सत्यापन हटा सकते हैं।',
};

/// Final map = English fallback ∪ Hindi overrides. Anything not in the
/// override map renders in English.
final Map<String, String> appStringsHi = {
  ...appStringsEn,
  ..._hiOverrides,
};
