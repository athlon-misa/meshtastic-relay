import 'dart:async';
import 'package:uuid/uuid.dart';
import '../models/message.dart';
import '../models/server_envelope.dart';
import 'websocket_service.dart';
import 'database_service.dart';

/// Manages sending and receiving messages, with local SQLite persistence.
class MessageService {
  final WebSocketService _webSocketService;
  final DatabaseService _databaseService;
  final String Function() _getUserHash;

  StreamSubscription? _messageSubscription;

  /// Stream of newly received messages.
  final _incomingController = StreamController<RelayMessage>.broadcast();
  Stream<RelayMessage> get incomingMessages => _incomingController.stream;

  /// Stream of message status updates (for sent messages).
  final _statusController =
      StreamController<MapEntry<String, MessageStatus>>.broadcast();
  Stream<MapEntry<String, MessageStatus>> get statusUpdates =>
      _statusController.stream;

  static const _uuid = Uuid();

  MessageService({
    required WebSocketService webSocketService,
    required DatabaseService databaseService,
    required String Function() getUserHash,
  })  : _webSocketService = webSocketService,
        _databaseService = databaseService,
        _getUserHash = getUserHash;

  /// Start listening for incoming messages from the WebSocket.
  void startListening() {
    _messageSubscription?.cancel();
    _messageSubscription = _webSocketService.messages.listen(_handleMessage);
  }

  void stopListening() {
    _messageSubscription?.cancel();
    _messageSubscription = null;
  }

  void _handleMessage(ServerEnvelope envelope) {
    if (envelope.type == MessageTypes.message) {
      _handleIncomingMessage(envelope.data);
    }
  }

  Future<void> _handleIncomingMessage(Map<String, dynamic> data) async {
    final message = RelayMessage(
      id: data['id'] as String? ?? _uuid.v4(),
      fromHash: data['from'] as String,
      toHash: _getUserHash(),
      text: data['text'] as String,
      timestamp: data['timestamp'] as int,
      fromName: data['from_name'] as String?,
      status: MessageStatus.delivered,
      isOutgoing: false,
    );

    // Persist to local database
    await _databaseService.insertMessage(message);

    // Notify listeners
    _incomingController.add(message);
  }

  /// Send a text message to a recipient.
  Future<RelayMessage> sendMessage({
    required String toHash,
    required String text,
  }) async {
    final message = RelayMessage(
      id: _uuid.v4(),
      fromHash: _getUserHash(),
      toHash: toHash,
      text: text,
      timestamp: DateTime.now().millisecondsSinceEpoch ~/ 1000,
      status: MessageStatus.sending,
      isOutgoing: true,
    );

    // Persist locally first
    await _databaseService.insertMessage(message);

    // Send via WebSocket
    try {
      _webSocketService.send(MessageTypes.sendMessage, {
        'to': toHash,
        'text': text,
      });

      // Mark as sent
      final sentMessage = message.copyWith(status: MessageStatus.sent);
      await _databaseService.updateMessageStatus(message.id, MessageStatus.sent);
      _statusController.add(MapEntry(message.id, MessageStatus.sent));
      return sentMessage;
    } catch (e) {
      // Mark as failed
      await _databaseService.updateMessageStatus(
          message.id, MessageStatus.failed);
      _statusController.add(MapEntry(message.id, MessageStatus.failed));
      return message.copyWith(status: MessageStatus.failed);
    }
  }

  /// Get all messages for a conversation with a specific peer.
  Future<List<RelayMessage>> getMessagesForPeer(String peerHash) async {
    return _databaseService.getMessagesForPeer(peerHash);
  }

  /// Get the most recent message for each conversation peer.
  Future<Map<String, RelayMessage>> getLatestMessages() async {
    return _databaseService.getLatestMessages();
  }

  void dispose() {
    stopListening();
    _incomingController.close();
    _statusController.close();
  }
}
