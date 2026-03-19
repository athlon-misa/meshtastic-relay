/// Represents a known contact on the relay network.
class Contact {
  final String userHash;
  final String? nodeId;
  final String? name;
  final bool isOnline;
  final int? onlineSince;

  const Contact({
    required this.userHash,
    this.nodeId,
    this.name,
    this.isOnline = false,
    this.onlineSince,
  });

  Contact copyWith({
    String? userHash,
    String? nodeId,
    String? name,
    bool? isOnline,
    int? onlineSince,
  }) {
    return Contact(
      userHash: userHash ?? this.userHash,
      nodeId: nodeId ?? this.nodeId,
      name: name ?? this.name,
      isOnline: isOnline ?? this.isOnline,
      onlineSince: onlineSince ?? this.onlineSince,
    );
  }

  /// Short hash for display.
  String get shortHash =>
      userHash.length > 12 ? userHash.substring(0, 12) : userHash;

  /// Display name: use name if set, otherwise short hash.
  String get displayName => name ?? shortHash;

  Map<String, dynamic> toMap() => {
        'user_hash': userHash,
        'node_id': nodeId,
        'name': name,
        'is_online': isOnline ? 1 : 0,
        'online_since': onlineSince,
      };

  factory Contact.fromMap(Map<String, dynamic> map) => Contact(
        userHash: map['user_hash'] as String,
        nodeId: map['node_id'] as String?,
        name: map['name'] as String?,
        isOnline: (map['is_online'] as int? ?? 0) == 1,
        onlineSince: map['online_since'] as int?,
      );

  @override
  String toString() => 'Contact($userHash, online: $isOnline)';
}
