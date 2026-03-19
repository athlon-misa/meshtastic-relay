import 'message.dart';

/// Represents a conversation with another user.
class Conversation {
  final String peerHash;
  final String? peerName;
  final RelayMessage? lastMessage;
  final int unreadCount;
  final bool isOnline;

  const Conversation({
    required this.peerHash,
    this.peerName,
    this.lastMessage,
    this.unreadCount = 0,
    this.isOnline = false,
  });

  Conversation copyWith({
    String? peerHash,
    String? peerName,
    RelayMessage? lastMessage,
    int? unreadCount,
    bool? isOnline,
  }) {
    return Conversation(
      peerHash: peerHash ?? this.peerHash,
      peerName: peerName ?? this.peerName,
      lastMessage: lastMessage ?? this.lastMessage,
      unreadCount: unreadCount ?? this.unreadCount,
      isOnline: isOnline ?? this.isOnline,
    );
  }

  /// Short peer hash for display (first 12 hex chars).
  String get shortPeerHash =>
      peerHash.length > 12 ? peerHash.substring(0, 12) : peerHash;

  /// Display name: use peerName if set, otherwise short hash.
  String get displayName => peerName ?? shortPeerHash;

  @override
  String toString() => 'Conversation($peerHash)';
}
