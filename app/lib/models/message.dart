/// Represents a chat message in the Relay system.
class RelayMessage {
  final String id;
  final String fromHash;
  final String toHash;
  final String text;
  final int timestamp;
  final String? fromName;
  final MessageStatus status;
  final bool isOutgoing;

  const RelayMessage({
    required this.id,
    required this.fromHash,
    required this.toHash,
    required this.text,
    required this.timestamp,
    this.fromName,
    this.status = MessageStatus.sent,
    this.isOutgoing = false,
  });

  RelayMessage copyWith({
    String? id,
    String? fromHash,
    String? toHash,
    String? text,
    int? timestamp,
    String? fromName,
    MessageStatus? status,
    bool? isOutgoing,
  }) {
    return RelayMessage(
      id: id ?? this.id,
      fromHash: fromHash ?? this.fromHash,
      toHash: toHash ?? this.toHash,
      text: text ?? this.text,
      timestamp: timestamp ?? this.timestamp,
      fromName: fromName ?? this.fromName,
      status: status ?? this.status,
      isOutgoing: isOutgoing ?? this.isOutgoing,
    );
  }

  DateTime get dateTime =>
      DateTime.fromMillisecondsSinceEpoch(timestamp * 1000);

  Map<String, dynamic> toMap() => {
        'id': id,
        'from_hash': fromHash,
        'to_hash': toHash,
        'text': text,
        'timestamp': timestamp,
        'from_name': fromName,
        'status': status.index,
        'is_outgoing': isOutgoing ? 1 : 0,
      };

  factory RelayMessage.fromMap(Map<String, dynamic> map) => RelayMessage(
        id: map['id'] as String,
        fromHash: map['from_hash'] as String,
        toHash: map['to_hash'] as String,
        text: map['text'] as String,
        timestamp: map['timestamp'] as int,
        fromName: map['from_name'] as String?,
        status: MessageStatus.values[map['status'] as int? ?? 0],
        isOutgoing: (map['is_outgoing'] as int? ?? 0) == 1,
      );

  @override
  String toString() => 'RelayMessage($id, from: $fromHash, text: $text)';
}

enum MessageStatus {
  sending,
  sent,
  delivered,
  failed,
}
