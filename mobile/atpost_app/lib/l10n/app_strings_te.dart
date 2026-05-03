// Telugu (te) Pulse strings.
//
// Translator instructions: replace each value in place; do not change keys.
// Strings not present in the override map fall back to English via the
// resolver. See `app_strings_hi.dart` for the pattern.
//
// Sprint 5: 5 high-priority Pulse strings translated by the in-house team
// for the Sprint-5 demo. The full sweep ships in Sprint 6.

import 'package:atpost_app/l10n/app_strings_en.dart';

const Map<String, String> _teOverrides = {
  // Onboarding intent picker title.
  'pulse.intent.title': 'మీరు దేని కోసం వెతుకుతున్నారు?',
  // Tune setup primary CTA.
  'pulse.tune.save': 'సేవ్ చేసి కొనసాగించండి',
  // "Spark sent" snackbar.
  'pulse.spark.sent': 'Spark పంపబడింది.',
  // "It's a match!" celebration.
  'pulse.match.celebration': 'మ్యాచ్ అయింది!',
  // Aadhaar disclosure copy.
  'pulse.aadhaar.disclosure':
      'Pulse మీ ఆధార్ నంబర్‌ను నిల్వ చేయదు లేదా పంపదు. మీ ప్రభుత్వ ఐడిలో ఉన్న '
          'పేరు, పుట్టిన తేదీ సరిపోలతాయా అని DigiLocker నుండి ఒక్కసారి మాత్రమే '
          'మాకు నిర్ధారణ అందుతుంది. మీరు Safety Center నుండి ఎప్పుడైనా '
          'వెరిఫికేషన్‌ను రద్దు చేయవచ్చు.',
};

final Map<String, String> appStringsTe = {
  ...appStringsEn,
  ..._teOverrides,
};
