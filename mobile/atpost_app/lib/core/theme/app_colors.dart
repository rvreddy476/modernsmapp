import 'package:flutter/material.dart';

class AppColors {
  const AppColors._();

  static const Color bgPrimary = Color(0xFF000000);
  static const Color bgSecondary = Color(0xFF121212);
  static const Color bgTertiary = Color(0xFF1C1C1E);
  static const Color bgCard = Color(0x0AFFFFFF);
  static const Color bgCardHover = Color(0x0FFFFFFF);

  static const Color borderSubtle = Color(0x0FFFFFFF);
  static const Color borderMedium = Color(0x14FFFFFF);

  static const Color textPrimary = Color(0xFFFFFFFF);
  static const Color textSecondary = Color(0xFFE5E5EA);
  static const Color textTertiary = Color(0xFFD1D1D6);
  static const Color textMuted = Color(0xFF8E8E93);
  static const Color textDim = Color(0xFF636366);
  static const Color textDimmest = Color(0xFF48484A);
  static const Color textGhost = Color(0xFF2C2C2E);

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

