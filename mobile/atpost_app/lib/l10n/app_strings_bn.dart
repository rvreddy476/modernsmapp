// Bengali (bn) Pulse strings.
//
// Translator instructions: replace each value in place; do not change keys.
// Strings not present in the override map fall back to English via the
// resolver. See `app_strings_hi.dart` for the pattern.
//
// Sprint 5: 5 high-priority Pulse strings translated by the in-house team
// for the Sprint-5 demo. The full sweep ships in Sprint 6.

import 'package:atpost_app/l10n/app_strings_en.dart';

const Map<String, String> _bnOverrides = {
  // Onboarding intent picker title.
  'pulse.intent.title': 'আপনি কী খুঁজছেন?',
  // Tune setup primary CTA.
  'pulse.tune.save': 'সংরক্ষণ করুন এবং চালিয়ে যান',
  // "Spark sent" snackbar.
  'pulse.spark.sent': 'Spark পাঠানো হয়েছে।',
  // "It's a match!" celebration.
  'pulse.match.celebration': 'এটা একটা ম্যাচ!',
  // Aadhaar disclosure copy.
  'pulse.aadhaar.disclosure':
      'Pulse আপনার আধার নম্বর কখনই সংরক্ষণ বা প্রেরণ করে না। আমরা DigiLocker '
          'থেকে শুধু একবার নিশ্চিতকরণ পাই যে আপনার নাম এবং জন্ম তারিখ আপনার '
          'সরকারি পরিচয়পত্রের সঙ্গে মিলে যাচ্ছে। আপনি যেকোনো সময় Safety '
          'Center থেকে যাচাইকরণ প্রত্যাহার করতে পারেন।',
};

final Map<String, String> appStringsBn = {
  ...appStringsEn,
  ..._bnOverrides,
};
