import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/providers.dart';
import '../theme/app_theme.dart';

/// First-launch screen: generate or import an Ed25519 identity.
class OnboardingScreen extends ConsumerStatefulWidget {
  const OnboardingScreen({super.key});

  @override
  ConsumerState<OnboardingScreen> createState() => _OnboardingScreenState();
}

class _OnboardingScreenState extends ConsumerState<OnboardingScreen> {
  bool _isGenerating = false;
  bool _showImport = false;
  final _importController = TextEditingController();
  final _serverController = TextEditingController(text: 'ws://localhost:9370/ws');

  @override
  void dispose() {
    _importController.dispose();
    _serverController.dispose();
    super.dispose();
  }

  Future<void> _generateIdentity() async {
    setState(() => _isGenerating = true);

    try {
      final identityService = ref.read(identityServiceProvider);
      final identity = await identityService.generateNew();

      ref.read(identityProvider.notifier).state = identity;
      ref.read(serverUrlProvider.notifier).state = _serverController.text;
      ref.read(isSetupCompleteProvider.notifier).state = true;
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to generate identity: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _isGenerating = false);
    }
  }

  Future<void> _importIdentity() async {
    final hexKey = _importController.text.trim();
    if (hexKey.isEmpty) return;

    setState(() => _isGenerating = true);

    try {
      final identityService = ref.read(identityServiceProvider);
      final identity = await identityService.importFromHex(hexKey);

      ref.read(identityProvider.notifier).state = identity;
      ref.read(serverUrlProvider.notifier).state = _serverController.text;
      ref.read(isSetupCompleteProvider.notifier).state = true;
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to import identity: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _isGenerating = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              const Spacer(flex: 2),

              // Logo/icon
              Container(
                width: 80,
                height: 80,
                decoration: BoxDecoration(
                  color: AppTheme.primaryBlue.withAlpha(26),
                  borderRadius: BorderRadius.circular(20),
                ),
                child: const Icon(
                  Icons.cell_tower_rounded,
                  size: 42,
                  color: AppTheme.primaryBlue,
                ),
              ),
              const SizedBox(height: 24),

              Text(
                'Relay',
                style: Theme.of(context).textTheme.headlineLarge,
              ),
              const SizedBox(height: 8),
              Text(
                'Secure mesh messaging',
                style: Theme.of(context).textTheme.bodyMedium,
              ),

              const Spacer(flex: 1),

              // Server URL field
              TextField(
                controller: _serverController,
                decoration: const InputDecoration(
                  labelText: 'Server address',
                  hintText: 'ws://localhost:9370/ws',
                  prefixIcon: Icon(Icons.dns_outlined, size: 20),
                ),
                style: const TextStyle(fontSize: 14),
              ),

              const SizedBox(height: 32),

              // Generate button
              SizedBox(
                width: double.infinity,
                height: 52,
                child: FilledButton(
                  onPressed: _isGenerating ? null : _generateIdentity,
                  style: FilledButton.styleFrom(
                    backgroundColor: AppTheme.primaryBlue,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(14),
                    ),
                  ),
                  child: _isGenerating
                      ? const SizedBox(
                          width: 22,
                          height: 22,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Text(
                          'Generate New Identity',
                          style: TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                ),
              ),

              const SizedBox(height: 16),

              // Import toggle
              TextButton(
                onPressed: () => setState(() => _showImport = !_showImport),
                child: Text(
                  _showImport
                      ? 'Hide import'
                      : 'Import existing identity',
                  style: const TextStyle(color: AppTheme.primaryBlue),
                ),
              ),

              if (_showImport) ...[
                const SizedBox(height: 12),
                TextField(
                  controller: _importController,
                  decoration: const InputDecoration(
                    labelText: 'Private key (hex)',
                    hintText: 'Paste your 64-byte Ed25519 key...',
                    prefixIcon: Icon(Icons.key_outlined, size: 20),
                  ),
                  style: const TextStyle(fontSize: 13, fontFamily: 'monospace'),
                  maxLines: 2,
                ),
                const SizedBox(height: 12),
                SizedBox(
                  width: double.infinity,
                  height: 48,
                  child: OutlinedButton(
                    onPressed: _isGenerating ? null : _importIdentity,
                    style: OutlinedButton.styleFrom(
                      side: const BorderSide(color: AppTheme.primaryBlue),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14),
                      ),
                    ),
                    child: const Text('Import Identity'),
                  ),
                ),
              ],

              const Spacer(flex: 2),

              Text(
                'Your identity is an Ed25519 keypair.\n'
                'It is stored securely on this device.',
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const SizedBox(height: 24),
            ],
          ),
        ),
      ),
    );
  }
}
