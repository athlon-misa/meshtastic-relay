import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/providers.dart';
import '../models/contact.dart';
import '../widgets/avatar_widget.dart';
import '../theme/app_theme.dart';
import 'chat_screen.dart';

/// Online contacts / users screen.
class ContactsScreen extends ConsumerStatefulWidget {
  const ContactsScreen({super.key});

  @override
  ConsumerState<ContactsScreen> createState() => _ContactsScreenState();
}

class _ContactsScreenState extends ConsumerState<ContactsScreen> {
  @override
  void initState() {
    super.initState();
    // Request online users on screen open
    Future.microtask(() {
      ref.read(presenceServiceProvider).requestOnlineUsers();
    });
  }

  @override
  Widget build(BuildContext context) {
    final presenceService = ref.watch(presenceServiceProvider);
    final contacts = presenceService.contacts.values.toList();

    // Sort: online first, then by hash
    contacts.sort((a, b) {
      if (a.isOnline != b.isOnline) return a.isOnline ? -1 : 1;
      return a.userHash.compareTo(b.userHash);
    });

    final onlineCount = contacts.where((c) => c.isOnline).length;

    // Also listen for real-time updates
    ref.listen(presenceUpdatesProvider, (_, __) {
      setState(() {}); // Rebuild when presence changes
    });

    ref.listen(onlineUsersProvider, (_, __) {
      setState(() {}); // Rebuild when online list updates
    });

    return Scaffold(
      appBar: AppBar(
        title: const Text('Contacts'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, size: 22),
            onPressed: () {
              ref.read(presenceServiceProvider).requestOnlineUsers();
            },
          ),
        ],
      ),
      body: contacts.isEmpty
          ? _buildEmptyState()
          : Column(
              children: [
                // Online count header
                Container(
                  width: double.infinity,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  color: AppTheme.cardDark.withAlpha(128),
                  child: Text(
                    '$onlineCount user${onlineCount == 1 ? '' : 's'} online',
                    style: const TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                      color: AppTheme.textSecondary,
                    ),
                  ),
                ),

                // Contact list
                Expanded(
                  child: ListView.separated(
                    itemCount: contacts.length,
                    separatorBuilder: (_, __) => Padding(
                      padding: const EdgeInsets.only(left: 80),
                      child: Divider(
                        height: 0.5,
                        color: AppTheme.dividerDark.withAlpha(128),
                      ),
                    ),
                    itemBuilder: (context, index) {
                      return _ContactTile(
                        contact: contacts[index],
                        onTap: () {
                          Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => ChatScreen(
                                peerHash: contacts[index].userHash,
                                peerName: contacts[index].name,
                                isOnline: contacts[index].isOnline,
                              ),
                            ),
                          );
                        },
                      );
                    },
                  ),
                ),
              ],
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
              Icons.people_outline,
              size: 64,
              color: AppTheme.textTertiary.withAlpha(77),
            ),
            const SizedBox(height: 16),
            Text(
              'No contacts discovered',
              style: Theme.of(context).textTheme.titleMedium?.copyWith(
                    color: AppTheme.textTertiary,
                  ),
            ),
            const SizedBox(height: 8),
            Text(
              'Users will appear here as they come online',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 24),
            OutlinedButton.icon(
              onPressed: () {
                ref.read(presenceServiceProvider).requestOnlineUsers();
              },
              icon: const Icon(Icons.refresh, size: 18),
              label: const Text('Refresh'),
            ),
          ],
        ),
      ),
    );
  }
}

class _ContactTile extends StatelessWidget {
  final Contact contact;
  final VoidCallback onTap;

  const _ContactTile({
    required this.contact,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          children: [
            AvatarWidget(
              userHash: contact.userHash,
              size: 48,
              showOnlineIndicator: true,
              isOnline: contact.isOnline,
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    contact.displayName,
                    style: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w500,
                      color: AppTheme.textPrimary,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      Container(
                        width: 7,
                        height: 7,
                        decoration: BoxDecoration(
                          color: contact.isOnline
                              ? AppTheme.onlineGreen
                              : AppTheme.textTertiary,
                          shape: BoxShape.circle,
                        ),
                      ),
                      const SizedBox(width: 6),
                      Text(
                        contact.isOnline ? 'Online' : 'Offline',
                        style: TextStyle(
                          fontSize: 13,
                          color: contact.isOnline
                              ? AppTheme.onlineGreen
                              : AppTheme.textTertiary,
                        ),
                      ),
                      if (contact.nodeId != null) ...[
                        const SizedBox(width: 12),
                        Text(
                          'Node: ${contact.nodeId!.length > 12 ? contact.nodeId!.substring(0, 12) : contact.nodeId}',
                          style: const TextStyle(
                            fontSize: 12,
                            color: AppTheme.textTertiary,
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
            const Icon(
              Icons.chevron_right,
              color: AppTheme.textTertiary,
              size: 20,
            ),
          ],
        ),
      ),
    );
  }
}
