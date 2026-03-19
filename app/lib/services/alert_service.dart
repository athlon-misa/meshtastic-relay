import 'dart:async';
import '../models/alert.dart';
import '../models/server_envelope.dart';
import 'websocket_service.dart';

/// Manages emergency broadcast alerts.
class AlertService {
  final WebSocketService _webSocketService;

  StreamSubscription? _messageSubscription;

  /// List of received alerts, most recent first.
  final List<RelayAlert> _alerts = [];

  /// Stream of new incoming alerts.
  final _alertController = StreamController<RelayAlert>.broadcast();
  Stream<RelayAlert> get alertStream => _alertController.stream;

  /// Stream of alert-sent confirmations (alert ID).
  final _sentController = StreamController<String>.broadcast();
  Stream<String> get sentConfirmations => _sentController.stream;

  AlertService({required WebSocketService webSocketService})
      : _webSocketService = webSocketService;

  /// All received alerts.
  List<RelayAlert> get alerts => List.unmodifiable(_alerts);

  /// Most recent active alert, if any.
  RelayAlert? get activeAlert => _alerts.isNotEmpty ? _alerts.first : null;

  /// Start listening for alert messages.
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
      case MessageTypes.alert:
        _handleAlert(envelope.data);
        break;
      case MessageTypes.alertSent:
        _handleAlertSent(envelope.data);
        break;
    }
  }

  void _handleAlert(Map<String, dynamic> data) {
    final alert = RelayAlert.fromJson(data);

    // Deduplicate by ID
    if (_alerts.any((a) => a.id == alert.id)) return;

    _alerts.insert(0, alert);
    _alertController.add(alert);
  }

  void _handleAlertSent(Map<String, dynamic> data) {
    final alertId = data['alert_id'] as String?;
    if (alertId != null) {
      _sentController.add(alertId);
    }
  }

  /// Send an emergency alert. Priority: 0=critical, 1=high, 2=normal.
  void sendAlert({required String text, required AlertPriority priority}) {
    _webSocketService.send(MessageTypes.sendAlert, {
      'text': text,
      'priority': priority.index,
    });
  }

  /// Dismiss an alert by ID.
  void dismissAlert(String alertId) {
    _alerts.removeWhere((a) => a.id == alertId);
  }

  /// Clear all alerts.
  void clearAlerts() {
    _alerts.clear();
  }

  void dispose() {
    stopListening();
    _alertController.close();
    _sentController.close();
  }
}
