import 'package:flutter/material.dart';
import '../theme/app_theme.dart';

/// Generates a deterministic avatar from a user's identity hash.
/// Uses the hash bytes to pick a background color and display initials.
class AvatarWidget extends StatelessWidget {
  final String userHash;
  final double size;
  final bool showOnlineIndicator;
  final bool isOnline;

  const AvatarWidget({
    super.key,
    required this.userHash,
    this.size = 48,
    this.showOnlineIndicator = false,
    this.isOnline = false,
  });

  /// Generate a consistent color from a hex hash.
  Color _colorFromHash(String hash) {
    if (hash.length < 6) return AppTheme.primaryBlue;
    final r = int.parse(hash.substring(0, 2), radix: 16);
    final g = int.parse(hash.substring(2, 4), radix: 16);
    final b = int.parse(hash.substring(4, 6), radix: 16);
    // Desaturate and darken for a muted look
    return HSLColor.fromColor(Color.fromRGBO(r, g, b, 1.0))
        .withSaturation(0.4)
        .withLightness(0.35)
        .toColor();
  }

  /// Get display text (first 2 hex chars, uppercased).
  String _initials(String hash) {
    if (hash.length < 2) return '??';
    return hash.substring(0, 2).toUpperCase();
  }

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        Container(
          width: size,
          height: size,
          decoration: BoxDecoration(
            color: _colorFromHash(userHash),
            shape: BoxShape.circle,
          ),
          child: Center(
            child: Text(
              _initials(userHash),
              style: TextStyle(
                color: Colors.white.withAlpha(230),
                fontSize: size * 0.38,
                fontWeight: FontWeight.w600,
                letterSpacing: 1,
              ),
            ),
          ),
        ),
        if (showOnlineIndicator && isOnline)
          Positioned(
            right: 0,
            bottom: 0,
            child: Container(
              width: size * 0.3,
              height: size * 0.3,
              decoration: BoxDecoration(
                color: AppTheme.onlineGreen,
                shape: BoxShape.circle,
                border: Border.all(
                  color: Theme.of(context).scaffoldBackgroundColor,
                  width: 2,
                ),
              ),
            ),
          ),
      ],
    );
  }
}
