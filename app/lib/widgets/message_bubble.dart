import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import '../models/message.dart';
import '../theme/app_theme.dart';

/// A chat message bubble, styled like Signal.
/// Sent messages: right-aligned, blue background.
/// Received messages: left-aligned, gray background.
class MessageBubble extends StatelessWidget {
  final RelayMessage message;

  const MessageBubble({super.key, required this.message});

  @override
  Widget build(BuildContext context) {
    final isOutgoing = message.isOutgoing;
    final timeStr = DateFormat('HH:mm').format(message.dateTime);

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 2),
      child: Row(
        mainAxisAlignment:
            isOutgoing ? MainAxisAlignment.end : MainAxisAlignment.start,
        children: [
          Container(
            constraints: BoxConstraints(
              maxWidth: MediaQuery.of(context).size.width * 0.75,
            ),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
            decoration: BoxDecoration(
              color: isOutgoing
                  ? AppTheme.sentBubble
                  : AppTheme.receivedBubble,
              borderRadius: BorderRadius.only(
                topLeft: const Radius.circular(18),
                topRight: const Radius.circular(18),
                bottomLeft:
                    isOutgoing ? const Radius.circular(18) : const Radius.circular(4),
                bottomRight:
                    isOutgoing ? const Radius.circular(4) : const Radius.circular(18),
              ),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  message.text,
                  style: TextStyle(
                    color: isOutgoing ? Colors.white : AppTheme.textPrimary,
                    fontSize: 15,
                    height: 1.35,
                  ),
                ),
                const SizedBox(height: 3),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      timeStr,
                      style: TextStyle(
                        color: isOutgoing
                            ? Colors.white.withAlpha(178)
                            : AppTheme.textTertiary,
                        fontSize: 11,
                      ),
                    ),
                    if (isOutgoing) ...[
                      const SizedBox(width: 4),
                      _StatusIcon(status: message.status),
                    ],
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _StatusIcon extends StatelessWidget {
  final MessageStatus status;

  const _StatusIcon({required this.status});

  @override
  Widget build(BuildContext context) {
    IconData icon;
    Color color;

    switch (status) {
      case MessageStatus.sending:
        icon = Icons.access_time;
        color = Colors.white.withAlpha(153);
        break;
      case MessageStatus.sent:
        icon = Icons.check;
        color = Colors.white.withAlpha(178);
        break;
      case MessageStatus.delivered:
        icon = Icons.done_all;
        color = Colors.white.withAlpha(230);
        break;
      case MessageStatus.failed:
        icon = Icons.error_outline;
        color = AppTheme.errorRed;
        break;
    }

    return Icon(icon, size: 14, color: color);
  }
}
