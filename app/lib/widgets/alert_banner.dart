import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import '../models/alert.dart';
import '../theme/app_theme.dart';

/// Emergency alert banner displayed at the top of the screen.
/// Styled with high visibility based on alert priority.
class AlertBanner extends StatelessWidget {
  final RelayAlert alert;
  final VoidCallback? onDismiss;

  const AlertBanner({
    super.key,
    required this.alert,
    this.onDismiss,
  });

  Color _priorityColor() {
    switch (alert.priority) {
      case AlertPriority.critical:
        return AppTheme.errorRed;
      case AlertPriority.high:
        return AppTheme.warningOrange;
      case AlertPriority.normal:
        return AppTheme.primaryBlue;
    }
  }

  IconData _priorityIcon() {
    switch (alert.priority) {
      case AlertPriority.critical:
        return Icons.warning_rounded;
      case AlertPriority.high:
        return Icons.priority_high_rounded;
      case AlertPriority.normal:
        return Icons.info_outline_rounded;
    }
  }

  @override
  Widget build(BuildContext context) {
    final color = _priorityColor();
    final timeStr = DateFormat('HH:mm').format(alert.dateTime);
    final fromShort = alert.fromHash.length > 12
        ? alert.fromHash.substring(0, 12)
        : alert.fromHash;

    return Container(
      margin: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: color.withAlpha(26),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: color.withAlpha(77), width: 1),
      ),
      child: Material(
        color: Colors.transparent,
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Icon(_priorityIcon(), color: color, size: 20),
                  const SizedBox(width: 8),
                  Text(
                    alert.priorityLabel,
                    style: TextStyle(
                      color: color,
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 1.2,
                    ),
                  ),
                  const Spacer(),
                  Text(
                    timeStr,
                    style: TextStyle(
                      color: color.withAlpha(178),
                      fontSize: 11,
                    ),
                  ),
                  if (onDismiss != null) ...[
                    const SizedBox(width: 8),
                    GestureDetector(
                      onTap: onDismiss,
                      child: Icon(Icons.close, color: color.withAlpha(153), size: 18),
                    ),
                  ],
                ],
              ),
              const SizedBox(height: 8),
              Text(
                alert.text,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 15,
                  height: 1.4,
                ),
              ),
              const SizedBox(height: 6),
              Text(
                'From: $fromShort',
                style: TextStyle(
                  color: AppTheme.textTertiary,
                  fontSize: 11,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
