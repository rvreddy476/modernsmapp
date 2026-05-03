// Marathi (mr) Pulse strings.
//
// Translator instructions: replace each value in place; do not change keys.
// Strings not present in the override map fall back to English via the
// resolver. See `app_strings_hi.dart` for the pattern.
//
// Sprint 5: 5 high-priority Pulse strings translated by the in-house team
// for the Sprint-5 demo. The full sweep ships in Sprint 6.

import 'package:atpost_app/l10n/app_strings_en.dart';

const Map<String, String> _mrOverrides = {
  // Onboarding intent picker title.
  'pulse.intent.title': 'तुम्ही काय शोधत आहात?',
  // Tune setup primary CTA.
  'pulse.tune.save': 'जतन करा आणि पुढे जा',
  // "Spark sent" snackbar.
  'pulse.spark.sent': 'Spark पाठवले.',
  // "It's a match!" celebration.
  'pulse.match.celebration': 'जुळणी झाली!',
  // Aadhaar disclosure copy.
  'pulse.aadhaar.disclosure':
      'Pulse तुमचा आधार क्रमांक कधीही साठवत नाही किंवा पाठवत नाही. तुमचे नाव '
          'आणि जन्मतारीख तुमच्या सरकारी ओळखपत्राशी जुळते का याची एकदाच पुष्टी '
          'आम्हाला DigiLocker कडून मिळते. तुम्ही Safety Center मधून पडताळणी '
          'कधीही रद्द करू शकता.',
};

final Map<String, String> appStringsMr = {
  ...appStringsEn,
  ..._mrOverrides,
};
