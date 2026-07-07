import 'package:flutter/material.dart';

/// Maps registry [ServiceApp.iconName] (lucide-style names from the spec) to
/// the closest Material icon. Centralized so adding a new app only requires
/// adding one entry here.
IconData iconForServiceName(String name) {
  switch (name) {
    case 'ShoppingBag':
      return Icons.shopping_bag_rounded;
    case 'Youtube':
      return Icons.play_circle_fill_rounded;
    case 'Clapperboard':
      return Icons.movie_creation_rounded;
    case 'UtensilsCrossed':
      return Icons.restaurant_rounded;
    case 'GraduationCap':
      return Icons.school_rounded;
    case 'Heart':
      return Icons.favorite_rounded;
    case 'HelpCircle':
    case 'CircleHelp':
      return Icons.help_outline_rounded;
    case 'Briefcase':
      return Icons.work_rounded;
    case 'Wallet':
      return Icons.account_balance_wallet_rounded;
    case 'Grid':
    case 'LayoutGrid':
      return Icons.grid_view_rounded;
    default:
      return Icons.apps_rounded;
  }
}
