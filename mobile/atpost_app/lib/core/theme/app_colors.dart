import 'package:flutter/material.dart';

class AppColors {
  const AppColors._();

  static const Color bgPrimary = Color(0xFF08080F);
  static const Color bgSecondary = Color(0xFF0D0D18);
  static const Color bgTertiary = Color(0xFF14141F);
  static const Color bgCard = Color(0x0AFFFFFF);
  static const Color bgCardHover = Color(0x0FFFFFFF);

  static const Color borderSubtle = Color(0x0FFFFFFF);
  static const Color borderMedium = Color(0x14FFFFFF);

  static const Color textPrimary = Color(0xFFE8E8F0);
  static const Color textSecondary = Color(0xFFC8C8D8);
  static const Color textTertiary = Color(0xFF8B8BA7);
  static const Color textMuted = Color(0xFF6B6B80);
  static const Color textDim = Color(0xFF5A5A72);
  static const Color textDimmest = Color(0xFF4A4A62);
  static const Color textGhost = Color(0xFF3A3A52);

  static const Color postbookPrimary = Color(0xFFFF6B35);
  static const Color postbookSecondary = Color(0xFFFF8F65);
  static const Color postgramPrimary = Color(0xFFFF3366);
  static const Color postgramSecondary = Color(0xFFC850C0);
  static const Color posttubePrimary = Color(0xFF4ECDC4);
  static const Color posttubeSecondary = Color(0xFF44B8B0);
  static const Color accentPurple = Color(0xFF7B68EE);

  static const Color onlineGreen = Color(0xFF4ECDC4);
  static const Color liveRed = Color(0xFFFF3366);

  static const Color statusError = Color(0xFFFF4757);
  static const Color statusWarning = Color(0xFFFFAB00);
  static const Color statusSuccess = Color(0xFF2ED573);

  static const Color glassBg = Color(0x1AFFFFFF);
  static const Color glassBorder = Color(0x14FFFFFF);

  static const LinearGradient postbookGradient = LinearGradient(
    colors: [postbookPrimary, postbookSecondary],
  );
  static const LinearGradient postgramGradient = LinearGradient(
    colors: [postgramPrimary, postbookPrimary],
  );
  static const LinearGradient posttubeGradient = LinearGradient(
    colors: [posttubePrimary, accentPurple],
  );
  static const LinearGradient storyRingGradient = LinearGradient(
    colors: [postbookPrimary, postgramPrimary, accentPurple],
  );
  static const LinearGradient ctaGradient = LinearGradient(
    colors: [postbookPrimary, postgramPrimary],
  );
}

