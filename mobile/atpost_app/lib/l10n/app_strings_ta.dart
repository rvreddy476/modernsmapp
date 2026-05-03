// Tamil (ta) Pulse strings.
//
// Translator instructions: replace each value in place; do not change keys.
// Strings not present in the override map fall back to English via the
// resolver. See `app_strings_hi.dart` for the pattern.
//
// Sprint 5: 5 high-priority Pulse strings translated by the in-house team
// for the Sprint-5 demo. The full sweep ships in Sprint 6.

import 'package:atpost_app/l10n/app_strings_en.dart';

const Map<String, String> _taOverrides = {
  // Onboarding intent picker title.
  'pulse.intent.title': 'நீங்கள் எதைத் தேடுகிறீர்கள்?',
  // Tune setup primary CTA.
  'pulse.tune.save': 'சேமித்து தொடரவும்',
  // "Spark sent" snackbar.
  'pulse.spark.sent': 'Spark அனுப்பப்பட்டது.',
  // "It's a match!" celebration.
  'pulse.match.celebration': 'பொருத்தம்!',
  // Aadhaar disclosure copy.
  'pulse.aadhaar.disclosure':
      'Pulse உங்கள் ஆதார் எண்ணை சேமிப்பதோ அனுப்புவதோ இல்லை. உங்கள் அரசு '
          'அடையாளத்துடன் உங்கள் பெயரும் பிறந்த தேதியும் பொருந்துகிறதா என்பதை '
          'DigiLocker-இலிருந்து ஒரு முறை மட்டுமே நாங்கள் பெறுகிறோம். '
          'பாதுகாப்பு மையத்திலிருந்து எப்போது வேண்டுமானாலும் சரிபார்ப்பை '
          'ரத்து செய்யலாம்.',
};

final Map<String, String> appStringsTa = {
  ...appStringsEn,
  ..._taOverrides,
};
