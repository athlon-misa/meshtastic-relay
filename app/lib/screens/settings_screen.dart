import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/providers.dart';
import '../services/websocket_service.dart' show ConnectionState;
import '../theme/app_theme.dart';

/// Settings screen: server config, identity info, power/update status.
class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  late TextEditingController _serverController;

  @override
  void initState() {
    super.initState();
    final serverUrl = ref.read(serverUrlProvider);
    _serverController = TextEditingController(text: serverUrl);
  }

  @override
  void dispose() {
    _serverController.dispose();
    super.dispose();
  }

  void _copyToClipboard(String text, String label) {
    Clipboard.setData(ClipboardData(text: text));
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('$label copied to clipboard')),
    );
  }

  Future<void> _exportIdentity() async {
    final identityService = ref.read(identityServiceProvider);
    final hexKey = await identityService.exportAsHex();
    if (hexKey != null && mounted) {
      showDialog(
        context: context,
        builder: (context) => AlertDialog(
          backgroundColor: AppTheme.cardDark,
          title: const Text('Identity Key'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'Store this key securely. Anyone with this key can impersonate you.',
                style: TextStyle(color: AppTheme.warningOrange, fontSize: 13),
              ),
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: AppTheme.backgroundDark,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: SelectableText(
                  hexKey,
                  style: const TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 11,
                    color: AppTheme.textSecondary,
                  ),
                ),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () {
                _copyToClipboard(hexKey, 'Private key');
                Navigator.pop(context);
              },
              child: const Text('Copy'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Done'),
            ),
          ],
        ),
      );
    }
  }

  void _updateServerUrl() {
    final newUrl = _serverController.text.trim();
    if (newUrl.isEmpty) return;

    ref.read(serverUrlProvider.notifier).state = newUrl;
    final ws = ref.read(webSocketServiceProvider);
    ws.disconnect().then((_) {
      ws.setServerUrl(newUrl);
      ws.connect();
    });

    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Server URL updated, reconnecting...')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final identity = ref.watch(identityProvider);
    final connectionState = ref.watch(connectionStateProvider);
    final authService = ref.read(authServiceProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Settings'),
      ),
      body: ListView(
        children: [
          const SizedBox(height: 8),

          // --- Identity section ---
          _SectionHeader(title: 'IDENTITY'),
          if (identity != null) ...[
            _SettingsTile(
              icon: Icons.fingerprint,
              title: 'User Hash',
              subtitle: identity.userHash,
              onTap: () => _copyToClipboard(identity.userHash, 'User hash'),
              trailing: const Icon(Icons.copy, size: 18, color: AppTheme.textTertiary),
            ),
            _SettingsTile(
              icon: Icons.vpn_key_outlined,
              title: 'Public Key',
              subtitle: identity.publicKeyHex,
              onTap: () =>
                  _copyToClipboard(identity.publicKeyHex, 'Public key'),
              trailing: const Icon(Icons.copy, size: 18, color: AppTheme.textTertiary),
            ),
            _SettingsTile(
              icon: Icons.download_outlined,
              title: 'Export Identity',
              subtitle: 'Back up your private key',
              onTap: _exportIdentity,
              trailing: const Icon(
                  Icons.chevron_right, size: 20, color: AppTheme.textTertiary),
            ),
          ],

          const SizedBox(height: 16),

          // --- Server section ---
          _SectionHeader(title: 'SERVER'),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _serverController,
                    decoration: const InputDecoration(
                      hintText: 'ws://host:port/ws',
                      prefixIcon: Icon(Icons.dns_outlined, size: 20),
                      isDense: true,
                    ),
                    style: const TextStyle(fontSize: 14),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: _updateServerUrl,
                  style: FilledButton.styleFrom(
                    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                  ),
                  child: const Text('Save'),
                ),
              ],
            ),
          ),
          connectionState.when(
            data: (state) => _SettingsTile(
              icon: Icons.cloud_outlined,
              title: 'Connection Status',
              subtitle: _connectionLabel(state),
              subtitleColor: _connectionColor(state),
            ),
            loading: () => _SettingsTile(
              icon: Icons.cloud_outlined,
              title: 'Connection Status',
              subtitle: 'Loading...',
            ),
            error: (_, __) => _SettingsTile(
              icon: Icons.cloud_off,
              title: 'Connection Status',
              subtitle: 'Error',
              subtitleColor: AppTheme.errorRed,
            ),
          ),
          if (authService.nodeId != null)
            _SettingsTile(
              icon: Icons.cell_tower,
              title: 'Node ID',
              subtitle: authService.nodeId!,
              onTap: () =>
                  _copyToClipboard(authService.nodeId!, 'Node ID'),
            ),
          if (authService.sessionId != null)
            _SettingsTile(
              icon: Icons.badge_outlined,
              title: 'Session ID',
              subtitle: authService.sessionId!,
            ),

          const SizedBox(height: 16),

          // --- Node status section ---
          _SectionHeader(title: 'NODE STATUS'),
          _SettingsTile(
            icon: Icons.bolt_outlined,
            title: 'Power Status',
            subtitle: 'View node power tier',
            onTap: () => _requestPowerStatus(),
            trailing: const Icon(
                Icons.chevron_right, size: 20, color: AppTheme.textTertiary),
          ),
          _SettingsTile(
            icon: Icons.system_update_outlined,
            title: 'Update Status',
            subtitle: 'View OTA update info',
            onTap: () => _requestUpdateStatus(),
            trailing: const Icon(
                Icons.chevron_right, size: 20, color: AppTheme.textTertiary),
          ),

          const SizedBox(height: 16),

          // --- About section ---
          _SectionHeader(title: 'ABOUT'),
          const _SettingsTile(
            icon: Icons.info_outline,
            title: 'Relay',
            subtitle: 'Meshtastic Relay v1.0.0',
          ),
          const _SettingsTile(
            icon: Icons.lock_outline,
            title: 'Encryption',
            subtitle: 'Ed25519 + XSalsa20-Poly1305',
          ),

          const SizedBox(height: 48),
        ],
      ),
    );
  }

  void _requestPowerStatus() {
    final ws = ref.read(webSocketServiceProvider);
    ws.send('power_status', {});
  }

  void _requestUpdateStatus() {
    final ws = ref.read(webSocketServiceProvider);
    ws.send('update_status', {});
  }

  String _connectionLabel(ConnectionState state) {
    switch (state) {
      case ConnectionState.disconnected:
        return 'Disconnected';
      case ConnectionState.connecting:
        return 'Connecting...';
      case ConnectionState.connected:
        return 'Connected (authenticating)';
      case ConnectionState.authenticating:
        return 'Authenticating...';
      case ConnectionState.authenticated:
        return 'Connected & Authenticated';
      case ConnectionState.reconnecting:
        return 'Reconnecting...';
    }
  }

  Color _connectionColor(ConnectionState state) {
    switch (state) {
      case ConnectionState.disconnected:
        return AppTheme.errorRed;
      case ConnectionState.authenticated:
        return AppTheme.onlineGreen;
      default:
        return AppTheme.warningOrange;
    }
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;

  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        title,
        style: const TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: AppTheme.textTertiary,
          letterSpacing: 1,
        ),
      ),
    );
  }
}

class _SettingsTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String subtitle;
  final Color? subtitleColor;
  final VoidCallback? onTap;
  final Widget? trailing;

  const _SettingsTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    this.subtitleColor,
    this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          children: [
            Icon(icon, size: 22, color: AppTheme.textSecondary),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: const TextStyle(
                      fontSize: 15,
                      fontWeight: FontWeight.w500,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    subtitle,
                    style: TextStyle(
                      fontSize: 13,
                      color: subtitleColor ?? AppTheme.textSecondary,
                      fontFamily: subtitle.length > 24 ? 'monospace' : null,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            if (trailing != null) trailing!,
          ],
        ),
      ),
    );
  }
}
