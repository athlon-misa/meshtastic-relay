import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../models/conversation.dart';
import '../models/alert.dart';
import '../providers/providers.dart';
import '../widgets/avatar_widget.dart';
import '../widgets/alert_banner.dart';
import '../widgets/connection_indicator.dart';
import '../theme/app_theme.dart';
import '../services/websocket_service.dart' show ConnectionState;
import 'chat_screen.dart';

/// Main conversations list screen (Signal-style).
class ConversationsScreen extends ConsumerStatefulWidget {
  const ConversationsScreen({super.key});

  @override
  ConsumerState<ConversationsScreen> createState() =>
      _ConversationsScreenState();
}

class _ConversationsScreenState extends ConsumerState<ConversationsScreen> {
  @override
  void initState() {
    super.initState();
    // Refresh conversations on launch
    Future.microtask(() {
      ref.read(conversationsProvider.notifier).refresh();
    });
  }

  void _openChat(Conversation conversation) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatScreen(
          peerHash: conversation.peerHash,
          peerName: conversation.displayName,
          isOnline: conversation.isOnline,
        ),
      ),
    );
  }

  void _openNewChat() {
    _showNewChatDialog();
  }

  void _showNewChatDialog() {
    final controller = TextEditingController();

    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: AppTheme.cardDark,
        title: const Text('New Message'),
        content: TextField(
          controller: controller,
          decoration: const InputDecoration(
            hintText: 'Enter recipient hash...',
            prefixIcon: Icon(Icons.person_outline, size: 20),
          ),
          style: const TextStyle(fontSize: 14, fontFamily: 'monospace'),
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              final hash = controller.text.trim();
              if (hash.isNotEmpty) {
                Navigator.pop(context);
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => ChatScreen(
                      peerHash: hash,
                      peerName: null,
                    ),
                  ),
                );
              }
            },
            child: const Text('Chat'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final conversations = ref.watch(conversationsProvider);
    final connectionState = ref.watch(connectionStateProvider);
    final activeAlert = ref.watch(activeAlertProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Relay'),
        actions: [
          connectionState.when(
            data: (state) => ConnectionIndicator(state: state),
            loading: () =>
                const ConnectionIndicator(state: ConnectionState.connecting),
            error: (_, __) =>
                const ConnectionIndicator(state: ConnectionState.disconnected),
          ),
          IconButton(
            icon: const Icon(Icons.search, size: 22),
            onPressed: () {
              // Future: search functionality
            },
          ),
        ],
      ),
      body: Column(
        children: [
          // Alert banner
          if (activeAlert != null)
            AlertBanner(
              alert: activeAlert,
              onDismiss: () {
                ref.read(activeAlertProvider.notifier).state = null;
              },
            ),

          // Conversations list
          Expanded(
            child: conversations.isEmpty
                ? _buildEmptyState()
                : ListView.separated(
                    itemCount: conversations.length,
                    separatorBuilder: (_, __) => Padding(
                      padding: const EdgeInsets.only(left: 80),
                      child: Divider(
                        height: 0.5,
                        color: AppTheme.dividerDark.withAlpha(128),
                      ),
                    ),
                    itemBuilder: (context, index) {
                      return _ConversationTile(
                        conversation: conversations[index],
                        onTap: () => _openChat(conversations[index]),
                      );
                    },
                  ),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: _openNewChat,
        child: const Icon(Icons.edit, size: 22),
      ),
    );
  }

  Widget _buildEmptyState() {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(48),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.forum_outlined,
              size: 64,
              color: AppTheme.textTertiary.withAlpha(77),
            ),
            const SizedBox(height: 16),
            Text(
              'No conversations yet',
              style: Theme.of(context).textTheme.titleMedium?.copyWith(
                    color: AppTheme.textTertiary,
                  ),
            ),
            const SizedBox(height: 8),
            Text(
              'Tap the compose button to start a new message',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ),
      ),
    );
  }
}

/// A single conversation row in the list.
class _ConversationTile extends StatelessWidget {
  final Conversation conversation;
  final VoidCallback onTap;

  const _ConversationTile({
    required this.conversation,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final lastMsg = conversation.lastMessage;
    final timeStr = lastMsg != null
        ? _formatTime(lastMsg.dateTime)
        : '';

    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          children: [
            // Avatar with online indicator
            AvatarWidget(
              userHash: conversation.peerHash,
              size: 52,
              showOnlineIndicator: true,
              isOnline: conversation.isOnline,
            ),
            const SizedBox(width: 14),

            // Name + last message
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          conversation.displayName,
                          style: const TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.w500,
                            color: AppTheme.textPrimary,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        timeStr,
                        style: TextStyle(
                          fontSize: 12,
                          color: conversation.unreadCount > 0
                              ? AppTheme.primaryBlue
                              : AppTheme.textTertiary,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          lastMsg?.text ?? '',
                          style: const TextStyle(
                            fontSize: 14,
                            color: AppTheme.textSecondary,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (conversation.unreadCount > 0) ...[
                        const SizedBox(width: 8),
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 7, vertical: 2),
                          decoration: BoxDecoration(
                            color: AppTheme.primaryBlue,
                            borderRadius: BorderRadius.circular(10),
                          ),
                          child: Text(
                            '${conversation.unreadCount}',
                            style: const TextStyle(
                              color: Colors.white,
                              fontSize: 12,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  String _formatTime(DateTime time) {
    final now = DateTime.now();
    final diff = now.difference(time);

    if (diff.inMinutes < 1) return 'now';
    if (diff.inHours < 1) return '${diff.inMinutes}m';
    if (diff.inDays < 1) return DateFormat('HH:mm').format(time);
    if (diff.inDays < 7) return DateFormat('EEE').format(time);
    return DateFormat('MM/dd').format(time);
  }
}
