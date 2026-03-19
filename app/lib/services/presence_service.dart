import 'dart:async';
import '../models/contact.dart';
import '../models/server_envelope.dart';
import 'websocket_service.dart';

/// Tracks online/offline presence of users on the relay network.
class PresenceService {
  final WebSocketService _webSocketService;

  StreamSubscription? _messageSubscription;

  /// Current set of known contacts and their online status.
  final Map<String, Contact> _contacts = {};

  /// Stream of presence updates.
  final _presenceController = StreamController<Contact>.broadcast();
  Stream<Contact> get presenceUpdates => _presenceController.stream;

  /// Stream of complete online user list responses.
  final _onlineListController = StreamController<List<Contact>>.broadcast();
  Stream<List<Contact>> get onlineListUpdates => _onlineListController.stream;

  PresenceService({required WebSocketService webSocketService})
      : _webSocketService = webSocketService;

  /// Get all known contacts.
  Map<String, Contact> get contacts => Map.unmodifiable(_contacts);

  /// Get only online contacts.
  List<Contact> get onlineContacts =>
      _contacts.values.where((c) => c.isOnline).toList();

  /// Check if a specific user is online.
  bool isOnline(String userHash) =>
      _contacts[userHash]?.isOnline ?? false;

  /// Start listening for presence messages.
  void startListening() {
    _messageSubscription?.cancel();
    _messageSubscription = _webSocketService.messages.listen(_handleMessage);
  }

  void stopListening() {
    _messageSubscription?.cancel();
    _messageSubscription = null;
  }

  void _handleMessage(ServerEnvelope envelope) {
    switch (envelope.type) {
      case MessageTypes.presence:
        _handlePresence(envelope.data);
        break;
      case MessageTypes.whoOnline:
        _handleWhoOnline(envelope.data);
        break;
      case MessageTypes.lookupUser:
        _handleLookupUser(envelope.data);
        break;
    }
  }

  void _handlePresence(Map<String, dynamic> data) {
    final userHash = data['user_hash'] as String;
    final nodeId = data['node_id'] as String?;
    final online = data['online'] as bool? ?? false;

    final contact = Contact(
      userHash: userHash,
      nodeId: nodeId,
      name: _contacts[userHash]?.name,
      isOnline: online,
    );

    _contacts[userHash] = contact;
    _presenceController.add(contact);
  }

  void _handleWhoOnline(Map<String, dynamic> data) {
    final usersList = data['users'] as List<dynamic>? ?? [];
    final contacts = <Contact>[];

    for (final userData in usersList) {
      final userMap = userData as Map<String, dynamic>;
      final userHash = userMap['user_hash'] as String;
      final contact = Contact(
        userHash: userHash,
        nodeId: userMap['node_id'] as String?,
        name: _contacts[userHash]?.name,
        isOnline: true,
        onlineSince: userMap['since'] as int?,
      );
      _contacts[userHash] = contact;
      contacts.add(contact);
    }

    _onlineListController.add(contacts);
  }

  void _handleLookupUser(Map<String, dynamic> data) {
    final userHash = data['user_hash'] as String;
    final online = data['online'] as bool? ?? false;

    final contact = Contact(
      userHash: userHash,
      nodeId: data['node_id'] as String?,
      name: _contacts[userHash]?.name,
      isOnline: online,
      onlineSince: data['since'] as int?,
    );

    _contacts[userHash] = contact;
    _presenceController.add(contact);
  }

  /// Request the list of all online users.
  void requestOnlineUsers() {
    _webSocketService.send(MessageTypes.clientWhoOnline, {});
  }

  /// Look up a specific user's presence.
  void lookupUser(String userHash) {
    _webSocketService.send(MessageTypes.clientLookupUser, {
      'user_hash': userHash,
    });
  }

  /// Set a display name for a contact (local-only, persisted later).
  void setContactName(String userHash, String name) {
    final existing = _contacts[userHash];
    if (existing != null) {
      _contacts[userHash] = existing.copyWith(name: name);
    } else {
      _contacts[userHash] = Contact(userHash: userHash, name: name);
    }
  }

  /// Clear all presence data (e.g., on disconnect).
  void clearPresence() {
    for (final hash in _contacts.keys.toList()) {
      _contacts[hash] = _contacts[hash]!.copyWith(isOnline: false);
    }
  }

  void dispose() {
    stopListening();
    _presenceController.close();
    _onlineListController.close();
  }
}
