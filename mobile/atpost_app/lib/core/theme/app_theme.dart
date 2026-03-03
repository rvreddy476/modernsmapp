import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

class AppTheme {
  const AppTheme._();

  static ThemeData darkTheme = ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    scaffoldBackgroundColor: AppColors.bgPrimary,
    canvasColor: AppColors.bgPrimary,
    colorScheme: const ColorScheme.dark(
      primary: AppColors.postbookPrimary,
      secondary: AppColors.postgramPrimary,
      surface: AppColors.bgSecondary,
      onSurface: AppColors.textSecondary,
      error: Colors.redAccent,
    ),
    textTheme: GoogleFonts.outfitTextTheme().apply(
      bodyColor: AppColors.textSecondary,
      displayColor: AppColors.textPrimary,
    ),
    appBarTheme: const AppBarTheme(
      backgroundColor: Colors.transparent,
      elevation: 0,
      centerTitle: false,
      foregroundColor: AppColors.textPrimary,
    ),
    dividerTheme: const DividerThemeData(
      color: AppColors.borderSubtle,
      thickness: 1,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: AppColors.bgCard,
      hintStyle: const TextStyle(color: AppColors.textGhost),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: AppColors.postbookPrimary),
      ),
    ),
  );

  static ThemeData lightTheme = ThemeData(
    useMaterial3: true,
    brightness: Brightness.light,
    scaffoldBackgroundColor: Colors.white,
    canvasColor: Colors.white,
    colorScheme: const ColorScheme.light(
      primary: AppColors.postbookPrimary,
      secondary: AppColors.postgramPrimary,
      surface: Colors.white,
      onSurface: Color(0xFF1A1A1A),
      error: Colors.redAccent,
    ),
    textTheme: GoogleFonts.outfitTextTheme().apply(
      bodyColor: const Color(0xFF1A1A1A),
      displayColor: const Color(0xFF111111),
    ),
    appBarTheme: const AppBarTheme(
      backgroundColor: Colors.transparent,
      elevation: 0,
      centerTitle: false,
      foregroundColor: Color(0xFF111111),
    ),
    dividerTheme: const DividerThemeData(
      color: Color(0x14000000),
      thickness: 1,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: const Color(0xFFF6F6F6),
      hintStyle: const TextStyle(color: Color(0x88000000)),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: Color(0x22000000)),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: Color(0x22000000)),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: const BorderSide(color: AppColors.postbookPrimary),
      ),
    ),
  );
}
