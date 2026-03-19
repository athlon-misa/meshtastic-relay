import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/status.dart' as ws_status;
import '../models/server_envelope.dart';

/// Connection state for the WebSocket.
enum ConnectionState {
  disconnected,
  connecting,
  connected,
  authenticating,
  authenticated,
  reconnecting,
}

/// Manages the persistent WebSocket connection to the relay node.
///
/// Features:
/// - Automatic reconnection with exponential backoff
/// - JSON message framing (ServerEnvelope)
/// - Connection state tracking
/// - Heartbeat support
class WebSocketService {
  WebSocketChannel? _channel;
  StreamSubscription? _subscription;
  Timer? _reconnectTimer;
  Timer? _heartbeatTimer;

  String _serverUrl = 'ws://localhost:9370/ws';
  ConnectionState _state = ConnectionState.disconnected;

  /// Stream of parsed server messages.
  final _messageController = StreamController<ServerEnvelope>.broadcast();

  /// Stream of connection state changes.
  final _stateController = StreamController<ConnectionState>.broadcast();

  /// Incoming message stream.
  Stream<ServerEnvelope> get messages => _messageController.stream;

  /// Connection state stream.
  Stream<ConnectionState> get stateStream => _stateController.stream;

  /// Current connection state.
  ConnectionState get state => _state;

  /// Whether currently connected (may or may not be authenticated).
  bool get isConnected =>
      _state == ConnectionState.connected ||
      _state == ConnectionState.authenticating ||
      _state == ConnectionState.authenticated;

  /// Whether fully authenticated.
  bool get isAuthenticated => _state == ConnectionState.authenticated;

  int _reconnectAttempts = 0;
  static const _maxReconnectDelay = Duration(seconds: 30);
  static const _heartbeatInterval = Duration(seconds: 30);

  /// Update the server URL.
  void setServerUrl(String url) {
    _serverUrl = url;
  }

  /// Get the current server URL.
  String get serverUrl => _serverUrl;

  /// Connect to the relay node WebSocket server.
  Future<void> connect({String? url}) async {
    if (url != null) _serverUrl = url;

    if (_state == ConnectionState.connecting ||
        _state == ConnectionState.connected ||
        _state == ConnectionState.authenticating ||
        _state == ConnectionState.authenticated) {
      return; // Already connected or connecting
    }

    _setState(ConnectionState.connecting);

    try {
      final uri = Uri.parse(_serverUrl);
      _channel = WebSocketChannel.connect(uri);

      // Wait for the connection to be ready
      await _channel!.ready;

      _setState(ConnectionState.connected);
      _reconnectAttempts = 0;

      _subscription = _channel!.stream.listen(
        _onData,
        onError: _onError,
        onDone: _onDone,
      );

      _startHeartbeat();
    } catch (e) {
      _setState(ConnectionState.disconnected);
      _scheduleReconnect();
    }
  }

  /// Disconnect from the server.
  Future<void> disconnect() async {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
    _subscription?.cancel();
    _subscription = null;

    if (_channel != null) {
      await _channel!.sink.close(ws_status.normalClosure);
      _channel = null;
    }

    _setState(ConnectionState.disconnected);
    _reconnectAttempts = 0;
  }

  /// Send a typed JSON message to the server.
  void send(String type, Map<String, dynamic> data) {
    if (_channel == null) return;

    final envelope = ServerEnvelope(type: type, data: data);
    _channel!.sink.add(envelope.toJson());
  }

  /// Send a raw JSON string.
  void sendRaw(String jsonString) {
    _channel?.sink.add(jsonString);
  }

  /// Mark the connection as authenticated (called by AuthService).
  void markAuthenticated() {
    _setState(ConnectionState.authenticated);
  }

  /// Mark as authenticating (called by AuthService).
  void markAuthenticating() {
    _setState(ConnectionState.authenticating);
  }

  void _onData(dynamic data) {
    if (data is! String) return;

    try {
      final envelope = ServerEnvelope.fromJson(data);
      _messageController.add(envelope);
    } catch (e) {
      print('WebSocketService: failed to parse message: $e');
    }
  }

  void _onError(dynamic error) {
    print('WebSocketService: error: $error');
    _handleDisconnect();
  }

  void _onDone() {
    _handleDisconnect();
  }

  void _handleDisconnect() {
    _subscription?.cancel();
    _subscription = null;
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
    _channel = null;

    if (_state != ConnectionState.disconnected) {
      _setState(ConnectionState.reconnecting);
      _scheduleReconnect();
    }
  }

  void _scheduleReconnect() {
    _reconnectTimer?.cancel();

    // Exponential backoff: 1s, 2s, 4s, 8s, 16s, 30s max
    final delay = Duration(
      milliseconds: (1000 * (1 << _reconnectAttempts))
          .clamp(1000, _maxReconnectDelay.inMilliseconds),
    );
    _reconnectAttempts++;

    _reconnectTimer = Timer(delay, () {
      _setState(ConnectionState.disconnected);
      connect();
    });
  }

  void _startHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = Timer.periodic(_heartbeatInterval, (_) {
      if (isAuthenticated) {
        send(MessageTypes.clientHeartbeat, {});
      }
    });
  }

  void _setState(ConnectionState newState) {
    if (_state == newState) return;
    _state = newState;
    _stateController.add(newState);
  }

  /// Clean up resources.
  void dispose() {
    disconnect();
    _messageController.close();
    _stateController.close();
  }
}
