/// Priority levels for emergency alerts, matching Go backend.
enum AlertPriority {
  critical, // 0
  high, // 1
  normal, // 2
}

/// Represents an emergency broadcast alert.
class RelayAlert {
  final String id;
  final AlertPriority priority;
  final String fromHash;
  final String? nodeId;
  final String text;
  final int timestamp;

  const RelayAlert({
    required this.id,
    required this.priority,
    required this.fromHash,
    this.nodeId,
    required this.text,
    required this.timestamp,
  });

  DateTime get dateTime =>
      DateTime.fromMillisecondsSinceEpoch(timestamp * 1000);

  String get priorityLabel {
    switch (priority) {
      case AlertPriority.critical:
        return 'EMERGENCY';
      case AlertPriority.high:
        return 'HIGH PRIORITY';
      case AlertPriority.normal:
        return 'ALERT';
    }
  }

  factory RelayAlert.fromJson(Map<String, dynamic> json) => RelayAlert(
        id: json['id'] as String,
        priority: AlertPriority.values[json['priority'] as int? ?? 2],
        fromHash: json['from'] as String,
        nodeId: json['node_id'] as String?,
        text: json['text'] as String,
        timestamp: json['timestamp'] as int,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'priority': priority.index,
        'from': fromHash,
        'node_id': nodeId,
        'text': text,
        'timestamp': timestamp,
      };

  @override
  String toString() => 'RelayAlert($id, priority: $priority, text: $text)';
}
