import 'dart:convert';

/// Top-level JSON wire format for all WebSocket messages.
/// Matches Go: `{"type": "...", "data": {...}}`
class ServerEnvelope {
  final String type;
  final Map<String, dynamic> data;

  const ServerEnvelope({
    required this.type,
    required this.data,
  });

  factory ServerEnvelope.fromJson(String jsonString) {
    final map = json.decode(jsonString) as Map<String, dynamic>;
    return ServerEnvelope(
      type: map['type'] as String,
      data: map['data'] as Map<String, dynamic>? ?? {},
    );
  }

  String toJson() {
    return json.encode({
      'type': type,
      'data': data,
    });
  }

  @override
  String toString() => 'ServerEnvelope(type: $type)';
}

// -----------------------------------------------------------------
// Protocol message type constants (matching Go server + CLI)
// -----------------------------------------------------------------

class MessageTypes {
  // Server -> Client
  static const challenge = 'challenge';
  static const authOk = 'auth_ok';
  static const authFail = 'auth_fail';
  static const message = 'message';
  static const presence = 'presence';
  static const heartbeat = 'heartbeat';
  static const error = 'error';
  static const alert = 'alert';
  static const alertSent = 'alert_sent';
  static const whoOnline = 'who_online';
  static const lookupUser = 'lookup_user';
  static const powerStatus = 'power_status';
  static const updateStatus = 'update_status';

  // Client -> Server
  static const authResponse = 'auth_response';
  static const sendMessage = 'send_message';
  static const sendAlert = 'send_alert';
  static const clientHeartbeat = 'heartbeat';
  static const clientWhoOnline = 'who_online';
  static const clientLookupUser = 'lookup_user';
  static const clientPowerStatus = 'power_status';
  static const clientUpdateStatus = 'update_status';
}
