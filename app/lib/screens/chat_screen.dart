import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/message.dart';
import '../providers/providers.dart';
import '../widgets/avatar_widget.dart';
import '../widgets/message_bubble.dart';
import '../theme/app_theme.dart';

/// Chat screen with a specific peer. Signal-style message bubbles.
class ChatScreen extends ConsumerStatefulWidget {
  final String peerHash;
  final String? peerName;
  final bool isOnline;

  const ChatScreen({
    super.key,
    required this.peerHash,
    this.peerName,
    this.isOnline = false,
  });

  @override
  ConsumerState<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends ConsumerState<ChatScreen> {
  final _textController = TextEditingController();
  final _scrollController = ScrollController();
  List<RelayMessage> _messages = [];
  StreamSubscription? _incomingSub;
  StreamSubscription? _statusSub;
  bool _isLoading = true;

  @override
  void initState() {
    super.initState();
    _loadMessages();
    _listenForNewMessages();
  }

  @override
  void dispose() {
    _textController.dispose();
    _scrollController.dispose();
    _incomingSub?.cancel();
    _statusSub?.cancel();
    super.dispose();
  }

  Future<void> _loadMessages() async {
    final messageService = ref.read(messageServiceProvider);
    final messages = await messageService.getMessagesForPeer(widget.peerHash);
    if (mounted) {
      setState(() {
        _messages = messages;
        _isLoading = false;
      });
      _scrollToBottom();
    }
  }

  void _listenForNewMessages() {
    final messageService = ref.read(messageServiceProvider);

    _incomingSub = messageService.incomingMessages.listen((msg) {
      if (msg.fromHash == widget.peerHash || msg.toHash == widget.peerHash) {
        setState(() => _messages.add(msg));
        _scrollToBottom();
      }
    });

    _statusSub = messageService.statusUpdates.listen((entry) {
      final idx = _messages.indexWhere((m) => m.id == entry.key);
      if (idx >= 0) {
        setState(() {
          _messages[idx] = _messages[idx].copyWith(status: entry.value);
        });
      }
    });
  }

  void _scrollToBottom() {
    Future.delayed(const Duration(milliseconds: 100), () {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  Future<void> _sendMessage() async {
    final text = _textController.text.trim();
    if (text.isEmpty) return;

    _textController.clear();

    final messageService = ref.read(messageServiceProvider);
    final sentMsg = await messageService.sendMessage(
      toHash: widget.peerHash,
      text: text,
    );

    setState(() => _messages.add(sentMsg));
    _scrollToBottom();

    // Refresh conversations list
    ref.read(conversationsProvider.notifier).onMessageSent(sentMsg);
  }

  String get _displayName => widget.peerName ??
      (widget.peerHash.length > 12
          ? widget.peerHash.substring(0, 12)
          : widget.peerHash);

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        title: Row(
          children: [
            AvatarWidget(
              userHash: widget.peerHash,
              size: 36,
              showOnlineIndicator: true,
              isOnline: widget.isOnline,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    _displayName,
                    style: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (widget.isOnline)
                    const Text(
                      'online',
                      style: TextStyle(
                        fontSize: 12,
                        color: AppTheme.onlineGreen,
                        fontWeight: FontWeight.w400,
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.more_vert, size: 22),
            onPressed: () {
              // Future: chat options menu
            },
          ),
        ],
      ),
      body: Column(
        children: [
          // Messages area
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _messages.isEmpty
                    ? _buildEmptyChat()
                    : ListView.builder(
                        controller: _scrollController,
                        padding: const EdgeInsets.only(top: 8, bottom: 8),
                        itemCount: _messages.length,
                        itemBuilder: (context, index) {
                          final msg = _messages[index];
                          final showDate = index == 0 ||
                              _shouldShowDate(
                                  _messages[index - 1].dateTime, msg.dateTime);

                          return Column(
                            children: [
                              if (showDate) _DateSeparator(date: msg.dateTime),
                              MessageBubble(message: msg),
                            ],
                          );
                        },
                      ),
          ),

          // Input bar
          _InputBar(
            controller: _textController,
            onSend: _sendMessage,
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyChat() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          AvatarWidget(
            userHash: widget.peerHash,
            size: 72,
          ),
          const SizedBox(height: 16),
          Text(
            _displayName,
            style: Theme.of(context).textTheme.titleMedium,
          ),
          const SizedBox(height: 8),
          Text(
            'Send a message to start the conversation',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
    );
  }

  bool _shouldShowDate(DateTime prev, DateTime current) {
    return prev.year != current.year ||
        prev.month != current.month ||
        prev.day != current.day;
  }
}

/// Date separator between message groups.
class _DateSeparator extends StatelessWidget {
  final DateTime date;

  const _DateSeparator({required this.date});

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    String label;

    if (date.year == now.year &&
        date.month == now.month &&
        date.day == now.day) {
      label = 'Today';
    } else if (date.year == now.year &&
        date.month == now.month &&
        date.day == now.day - 1) {
      label = 'Yesterday';
    } else {
      label = '${date.month}/${date.day}/${date.year}';
    }

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          decoration: BoxDecoration(
            color: AppTheme.cardDark,
            borderRadius: BorderRadius.circular(12),
          ),
          child: Text(
            label,
            style: const TextStyle(
              color: AppTheme.textTertiary,
              fontSize: 12,
              fontWeight: FontWeight.w500,
            ),
          ),
        ),
      ),
    );
  }
}

/// Text input bar at the bottom of the chat screen.
class _InputBar extends StatelessWidget {
  final TextEditingController controller;
  final VoidCallback onSend;

  const _InputBar({
    required this.controller,
    required this.onSend,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 8,
        top: 8,
        bottom: MediaQuery.of(context).padding.bottom + 8,
      ),
      decoration: BoxDecoration(
        color: AppTheme.backgroundDark,
        border: Border(
          top: BorderSide(
            color: AppTheme.dividerDark.withAlpha(128),
            width: 0.5,
          ),
        ),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: TextField(
              controller: controller,
              maxLines: 4,
              minLines: 1,
              decoration: const InputDecoration(
                hintText: 'Message',
                border: InputBorder.none,
                contentPadding:
                    EdgeInsets.symmetric(horizontal: 16, vertical: 10),
              ),
              style: const TextStyle(fontSize: 16),
              textInputAction: TextInputAction.send,
              onSubmitted: (_) => onSend(),
            ),
          ),
          const SizedBox(width: 4),
          ValueListenableBuilder(
            valueListenable: controller,
            builder: (context, value, child) {
              final hasText = value.text.trim().isNotEmpty;
              return AnimatedContainer(
                duration: const Duration(milliseconds: 150),
                child: IconButton(
                  onPressed: hasText ? onSend : null,
                  icon: Icon(
                    Icons.send_rounded,
                    color: hasText ? AppTheme.primaryBlue : AppTheme.textTertiary,
                    size: 24,
                  ),
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}
