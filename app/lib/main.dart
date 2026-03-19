import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'theme/app_theme.dart';
import 'providers/providers.dart';
import 'screens/onboarding_screen.dart';
import 'screens/home_screen.dart';
import 'services/websocket_service.dart' show ConnectionState;

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(
    const ProviderScope(
      child: RelayApp(),
    ),
  );
}

class RelayApp extends ConsumerWidget {
  const RelayApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return MaterialApp(
      title: 'Relay',
      debugShowCheckedModeBanner: false,
      theme: AppTheme.lightTheme,
      darkTheme: AppTheme.darkTheme,
      themeMode: ThemeMode.dark,
      home: const AppBootstrap(),
    );
  }
}

/// Bootstrap widget that initializes services and routes to either
/// onboarding or the main app.
class AppBootstrap extends ConsumerStatefulWidget {
  const AppBootstrap({super.key});

  @override
  ConsumerState<AppBootstrap> createState() => _AppBootstrapState();
}

class _AppBootstrapState extends ConsumerState<AppBootstrap>
    with WidgetsBindingObserver {
  bool _isInitializing = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _initialize();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final ws = ref.read(webSocketServiceProvider);

    switch (state) {
      case AppLifecycleState.resumed:
        // Reconnect when app returns to foreground
        if (!ws.isConnected && ref.read(isSetupCompleteProvider)) {
          _connectToServer();
        }
        break;
      case AppLifecycleState.paused:
      case AppLifecycleState.inactive:
      case AppLifecycleState.detached:
      case AppLifecycleState.hidden:
        break;
    }
  }

  Future<void> _initialize() async {
    try {
      // 1. Initialize database
      final db = ref.read(databaseServiceProvider);
      await db.initialize();

      // 2. Check for existing identity
      final identityService = ref.read(identityServiceProvider);
      final existingIdentity = await identityService.initialize();

      if (existingIdentity != null) {
        ref.read(identityProvider.notifier).state = existingIdentity;
        ref.read(isSetupCompleteProvider.notifier).state = true;

        // 3. Connect to server
        await _connectToServer();
      }
    } catch (e) {
      print('Initialization error: $e');
    } finally {
      if (mounted) {
        setState(() => _isInitializing = false);
      }
    }
  }

  Future<void> _connectToServer() async {
    final ws = ref.read(webSocketServiceProvider);
    final authService = ref.read(authServiceProvider);
    final messageService = ref.read(messageServiceProvider);
    final presenceService = ref.read(presenceServiceProvider);
    final alertService = ref.read(alertServiceProvider);
    final serverUrl = ref.read(serverUrlProvider);

    // Start all service listeners
    authService.startListening();
    messageService.startListening();
    presenceService.startListening();
    alertService.startListening();

    // Listen for connection state to re-auth on reconnect
    ws.stateStream.listen((state) {
      if (state == ConnectionState.connected) {
        // Auth will be triggered automatically when challenge is received
        authService.reset();
      }
    });

    // Connect
    ws.setServerUrl(serverUrl);
    await ws.connect();
  }

  @override
  Widget build(BuildContext context) {
    final isSetup = ref.watch(isSetupCompleteProvider);

    // When setup transitions from incomplete to complete (onboarding finishes),
    // trigger the server connection.
    ref.listen(isSetupCompleteProvider, (previous, next) {
      if (previous == false && next == true) {
        _connectToServer();
      }
    });

    if (_isInitializing) {
      return const Scaffold(
        body: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              CircularProgressIndicator(
                color: AppTheme.primaryBlue,
              ),
              SizedBox(height: 16),
              Text(
                'Loading...',
                style: TextStyle(color: AppTheme.textSecondary),
              ),
            ],
          ),
        ),
      );
    }

    if (!isSetup) {
      return const OnboardingScreen();
    }

    return const HomeScreen();
  }
}
