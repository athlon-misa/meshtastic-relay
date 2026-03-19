import 'package:flutter/material.dart';
import '../services/websocket_service.dart' show ConnectionState;
import '../theme/app_theme.dart';

/// Subtle connection status indicator for the app bar.
class ConnectionIndicator extends StatelessWidget {
  final ConnectionState state;

  const ConnectionIndicator({super.key, required this.state});

  @override
  Widget build(BuildContext context) {
    Color color;
    String label;
    IconData icon;

    switch (state) {
      case ConnectionState.disconnected:
        color = AppTheme.errorRed;
        label = 'Disconnected';
        icon = Icons.cloud_off;
        break;
      case ConnectionState.connecting:
      case ConnectionState.reconnecting:
        color = AppTheme.warningOrange;
        label = 'Connecting...';
        icon = Icons.cloud_sync;
        break;
      case ConnectionState.connected:
      case ConnectionState.authenticating:
        color = AppTheme.warningOrange;
        label = 'Authenticating...';
        icon = Icons.cloud_sync;
        break;
      case ConnectionState.authenticated:
        color = AppTheme.onlineGreen;
        label = 'Connected';
        icon = Icons.cloud_done;
        break;
    }

    return Tooltip(
      message: label,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        child: Icon(icon, color: color, size: 20),
      ),
    );
  }
}
