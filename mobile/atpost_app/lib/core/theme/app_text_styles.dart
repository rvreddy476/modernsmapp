import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

class AppTextStyles {
  const AppTextStyles._();

  static TextStyle logo = GoogleFonts.outfit(
    fontWeight: FontWeight.w900,
    fontSize: 26,
    color: AppColors.textPrimary,
  );

  static TextStyle h1 = GoogleFonts.outfit(
    fontWeight: FontWeight.w900,
    fontSize: 28,
    color: AppColors.textPrimary,
  );

  static TextStyle h2 = GoogleFonts.outfit(
    fontWeight: FontWeight.w700,
    fontSize: 17,
    color: AppColors.textPrimary,
  );

  static TextStyle h3 = GoogleFonts.outfit(
    fontWeight: FontWeight.w700,
    fontSize: 15,
    color: AppColors.textPrimary,
  );

  static TextStyle body = GoogleFonts.outfit(
    fontWeight: FontWeight.w400,
    fontSize: 14.5,
    height: 1.55,
    color: AppColors.textSecondary,
  );

  static TextStyle bodyMedium = GoogleFonts.outfit(
    fontWeight: FontWeight.w500,
    fontSize: 14,
    color: AppColors.textSecondary,
  );

  static TextStyle bodySmall = GoogleFonts.outfit(
    fontWeight: FontWeight.w500,
    fontSize: 13,
    color: AppColors.textTertiary,
  );

  static TextStyle label = GoogleFonts.outfit(
    fontWeight: FontWeight.w600,
    fontSize: 13,
    color: AppColors.textSecondary,
  );

  static TextStyle labelSmall = GoogleFonts.outfit(
    fontWeight: FontWeight.w600,
    fontSize: 11,
    color: AppColors.textMuted,
  );

  static TextStyle labelTiny = GoogleFonts.outfit(
    fontWeight: FontWeight.w700,
    fontSize: 10,
    color: AppColors.textMuted,
  );

  static TextStyle mono = GoogleFonts.spaceMono(
    fontSize: 12,
    color: AppColors.textTertiary,
  );

  static TextStyle monoSmall = GoogleFonts.spaceMono(
    fontSize: 10,
    color: AppColors.textMuted,
  );

  static TextStyle tag = GoogleFonts.spaceMono(
    fontSize: 12,
    color: AppColors.posttubePrimary,
  );
}

