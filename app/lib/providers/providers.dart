import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../services/identity_service.dart';
import '../services/websocket_service.dart';
import '../services/auth_service.dart';
import '../services/message_service.dart';
import '../services/presence_service.dart';
import '../services/alert_service.dart';
import '../services/database_service.dart';
import '../models/identity.dart';
import '../models/message.dart';
import '../models/conversation.dart';
import '../models/contact.dart';
import '../models/alert.dart';

// -----------------------------------------------------------------
// Core service providers (singletons)
// -----------------------------------------------------------------

final databaseServiceProvider = Provider<DatabaseService>((ref) {
  final service = DatabaseService();
  ref.onDispose(() => service.close());
  return service;
});

final identityServiceProvider = Provider<IdentityService>((ref) {
  return IdentityService();
});

final webSocketServiceProvider = Provider<WebSocketService>((ref) {
  final service = WebSocketService();
  ref.onDispose(() => service.dispose());
  return service;
});

final authServiceProvider = Provider<AuthService>((ref) {
  final identityService = ref.watch(identityServiceProvider);
  final webSocketService = ref.watch(webSocketServiceProvider);
  final service = AuthService(
    identityService: identityService,
    webSocketService: webSocketService,
  );
  ref.onDispose(() => service.dispose());
  return service;
});

final messageServiceProvider = Provider<MessageService>((ref) {
  final webSocketService = ref.watch(webSocketServiceProvider);
  final databaseService = ref.watch(databaseServiceProvider);
  final authService = ref.watch(authServiceProvider);
  final service = MessageService(
    webSocketService: webSocketService,
    databaseService: databaseService,
    getUserHash: () => authService.userHash ?? '',
  );
  ref.onDispose(() => service.dispose());
  return service;
});

final presenceServiceProvider = Provider<PresenceService>((ref) {
  final webSocketService = ref.watch(webSocketServiceProvider);
  final service = PresenceService(webSocketService: webSocketService);
  ref.onDispose(() => service.dispose());
  return service;
});

final alertServiceProvider = Provider<AlertService>((ref) {
  final webSocketService = ref.watch(webSocketServiceProvider);
  final service = AlertService(webSocketService: webSocketService);
  ref.onDispose(() => service.dispose());
  return service;
});

// -----------------------------------------------------------------
// State providers
// -----------------------------------------------------------------

/// Current identity.
final identityProvider = StateProvider<RelayIdentity?>((ref) => null);

/// Whether the app has been set up (identity exists).
final isSetupCompleteProvider = StateProvider<bool>((ref) => false);

/// WebSocket connection state.
final connectionStateProvider =
    StreamProvider<ConnectionState>((ref) {
  final ws = ref.watch(webSocketServiceProvider);
  return ws.stateStream;
});

/// Auth state stream.
final authStateProvider = StreamProvider<AuthState>((ref) {
  final auth = ref.watch(authServiceProvider);
  return auth.stateStream;
});

/// Server URL setting.
final serverUrlProvider = StateProvider<String>((ref) => 'ws://localhost:9370/ws');

// -----------------------------------------------------------------
// Message & conversation providers
// -----------------------------------------------------------------

/// Stream of incoming messages.
final incomingMessagesProvider = StreamProvider<RelayMessage>((ref) {
  final messageService = ref.watch(messageServiceProvider);
  return messageService.incomingMessages;
});

/// Messages for a specific peer conversation.
final conversationMessagesProvider =
    FutureProvider.family<List<RelayMessage>, String>((ref, peerHash) async {
  final messageService = ref.watch(messageServiceProvider);
  return messageService.getMessagesForPeer(peerHash);
});

/// All conversations (derived from latest messages + presence).
final conversationsProvider =
    StateNotifierProvider<ConversationsNotifier, List<Conversation>>((ref) {
  final messageService = ref.watch(messageServiceProvider);
  final presenceService = ref.watch(presenceServiceProvider);
  return ConversationsNotifier(
    messageService: messageService,
    presenceService: presenceService,
  );
});

class ConversationsNotifier extends StateNotifier<List<Conversation>> {
  final MessageService _messageService;
  final PresenceService _presenceService;
  StreamSubscription? _messageSub;
  StreamSubscription? _presenceSub;

  ConversationsNotifier({
    required MessageService messageService,
    required PresenceService presenceService,
  })  : _messageService = messageService,
        _presenceService = presenceService,
        super([]) {
    _messageSub = _messageService.incomingMessages.listen((_) => refresh());
    _presenceSub =
        _presenceService.presenceUpdates.listen((_) => _updatePresence());
  }

  Future<void> refresh() async {
    final latestMessages = await _messageService.getLatestMessages();
    final conversations = <Conversation>[];

    for (final entry in latestMessages.entries) {
      final peerHash = entry.key;
      final lastMessage = entry.value;
      final isOnline = _presenceService.isOnline(peerHash);
      final contact = _presenceService.contacts[peerHash];

      conversations.add(Conversation(
        peerHash: peerHash,
        peerName: contact?.name,
        lastMessage: lastMessage,
        isOnline: isOnline,
      ));
    }

    // Sort by most recent message first
    conversations
        .sort((a, b) {
          final aTs = a.lastMessage?.timestamp ?? 0;
          final bTs = b.lastMessage?.timestamp ?? 0;
          return bTs.compareTo(aTs);
        });

    state = conversations;
  }

  void _updatePresence() {
    // Update online status for existing conversations
    state = state.map((conv) {
      final isOnline = _presenceService.isOnline(conv.peerHash);
      if (conv.isOnline != isOnline) {
        return conv.copyWith(isOnline: isOnline);
      }
      return conv;
    }).toList();
  }

  /// Add or update a conversation when a message is sent.
  void onMessageSent(RelayMessage message) {
    refresh();
  }

  @override
  void dispose() {
    _messageSub?.cancel();
    _presenceSub?.cancel();
    super.dispose();
  }
}

// -----------------------------------------------------------------
// Presence providers
// -----------------------------------------------------------------

/// Stream of presence changes.
final presenceUpdatesProvider = StreamProvider<Contact>((ref) {
  final presence = ref.watch(presenceServiceProvider);
  return presence.presenceUpdates;
});

/// Online users list.
final onlineUsersProvider = StreamProvider<List<Contact>>((ref) {
  final presence = ref.watch(presenceServiceProvider);
  return presence.onlineListUpdates;
});

// -----------------------------------------------------------------
// Alert providers
// -----------------------------------------------------------------

/// Stream of new alerts.
final alertStreamProvider = StreamProvider<RelayAlert>((ref) {
  final alertService = ref.watch(alertServiceProvider);
  return alertService.alertStream;
});

/// Active alert (most recent, if any).
final activeAlertProvider = StateProvider<RelayAlert?>((ref) {
  ref.listen(alertStreamProvider, (prev, next) {
    next.whenData((alert) {
      ref.controller.state = alert;
    });
  });
  return null;
});
