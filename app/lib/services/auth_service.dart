import 'dart:async';
import 'dart:typed_data';
import 'package:convert/convert.dart';
import '../models/server_envelope.dart';
import 'identity_service.dart';
import 'websocket_service.dart';

/// Authentication state.
enum AuthState {
  unauthenticated,
  challengeReceived,
  responseSent,
  authenticated,
  failed,
}

/// Handles the challenge-response authentication flow.
///
/// Protocol flow (matching Go server):
/// 1. Server sends `challenge` with `nonce` (hex) and `node_id` (hex)
/// 2. Client signs the nonce bytes with Ed25519 private key
/// 3. Client sends `auth_response` with `public_key` (hex) and `signature` (hex)
/// 4. Server replies `auth_ok` with `user_hash` and `session_id`
///    OR `auth_fail` with `reason`
class AuthService {
  final IdentityService _identityService;
  final WebSocketService _webSocketService;

  AuthState _state = AuthState.unauthenticated;
  String? _userHash;
  String? _sessionId;
  String? _nodeId;
  String? _failureReason;

  StreamSubscription? _messageSubscription;
  final _authStateController = StreamController<AuthState>.broadcast();

  AuthService({
    required IdentityService identityService,
    required WebSocketService webSocketService,
  })  : _identityService = identityService,
        _webSocketService = webSocketService;

  AuthState get state => _state;
  String? get userHash => _userHash;
  String? get sessionId => _sessionId;
  String? get nodeId => _nodeId;
  String? get failureReason => _failureReason;

  Stream<AuthState> get stateStream => _authStateController.stream;

  /// Start listening for auth-related messages from the WebSocket.
  void startListening() {
    _messageSubscription?.cancel();
    _messageSubscription = _webSocketService.messages.listen(_handleMessage);
  }

  /// Stop listening.
  void stopListening() {
    _messageSubscription?.cancel();
    _messageSubscription = null;
  }

  void _handleMessage(ServerEnvelope envelope) {
    switch (envelope.type) {
      case MessageTypes.challenge:
        _handleChallenge(envelope.data);
        break;
      case MessageTypes.authOk:
        _handleAuthOk(envelope.data);
        break;
      case MessageTypes.authFail:
        _handleAuthFail(envelope.data);
        break;
    }
  }

  /// Handle the challenge from the server: sign the nonce and send auth_response.
  Future<void> _handleChallenge(Map<String, dynamic> data) async {
    _setState(AuthState.challengeReceived);
    _webSocketService.markAuthenticating();

    final nonceHex = data['nonce'] as String?;
    _nodeId = data['node_id'] as String?;

    if (nonceHex == null) {
      _setState(AuthState.failed);
      _failureReason = 'Missing nonce in challenge';
      return;
    }

    if (!_identityService.hasIdentity) {
      _setState(AuthState.failed);
      _failureReason = 'No identity loaded';
      return;
    }

    try {
      // Decode the hex nonce to bytes
      final nonceBytes = Uint8List.fromList(hex.decode(nonceHex));

      // Sign the nonce with our Ed25519 private key
      final signature = await _identityService.sign(nonceBytes);

      // Send auth_response with public_key and signature (both hex-encoded)
      _webSocketService.send(MessageTypes.authResponse, {
        'public_key': _identityService.identity!.publicKeyHex,
        'signature': hex.encode(signature),
      });

      _setState(AuthState.responseSent);
    } catch (e) {
      _setState(AuthState.failed);
      _failureReason = 'Failed to sign challenge: $e';
    }
  }

  /// Handle auth_ok from the server.
  void _handleAuthOk(Map<String, dynamic> data) {
    _userHash = data['user_hash'] as String?;
    _sessionId = data['session_id'] as String?;
    _setState(AuthState.authenticated);
    _webSocketService.markAuthenticated();
  }

  /// Handle auth_fail from the server.
  void _handleAuthFail(Map<String, dynamic> data) {
    _failureReason = data['reason'] as String? ?? 'Unknown failure';
    _setState(AuthState.failed);
  }

  /// Reset auth state (e.g., on reconnect).
  void reset() {
    _state = AuthState.unauthenticated;
    _userHash = null;
    _sessionId = null;
    _nodeId = null;
    _failureReason = null;
    _authStateController.add(_state);
  }

  void _setState(AuthState newState) {
    _state = newState;
    _authStateController.add(newState);
  }

  void dispose() {
    stopListening();
    _authStateController.close();
  }
}
