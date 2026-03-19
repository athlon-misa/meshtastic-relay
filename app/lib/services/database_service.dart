import 'package:sqflite/sqflite.dart';
import 'package:path_provider/path_provider.dart';
import 'package:path/path.dart' as path;
import '../models/message.dart';
import '../models/contact.dart';

/// SQLite database service for local message and contact persistence.
class DatabaseService {
  Database? _db;

  /// Initialize the database.
  Future<void> initialize() async {
    if (_db != null) return;

    final dir = await getApplicationDocumentsDirectory();
    final dbPath = path.join(dir.path, 'relay.db');

    _db = await openDatabase(
      dbPath,
      version: 1,
      onCreate: _onCreate,
    );
  }

  Future<void> _onCreate(Database db, int version) async {
    await db.execute('''
      CREATE TABLE messages (
        id TEXT PRIMARY KEY,
        from_hash TEXT NOT NULL,
        to_hash TEXT NOT NULL,
        text TEXT NOT NULL,
        timestamp INTEGER NOT NULL,
        from_name TEXT,
        status INTEGER NOT NULL DEFAULT 0,
        is_outgoing INTEGER NOT NULL DEFAULT 0
      )
    ''');

    await db.execute('''
      CREATE INDEX idx_messages_from ON messages(from_hash)
    ''');

    await db.execute('''
      CREATE INDEX idx_messages_to ON messages(to_hash)
    ''');

    await db.execute('''
      CREATE INDEX idx_messages_timestamp ON messages(timestamp DESC)
    ''');

    await db.execute('''
      CREATE TABLE contacts (
        user_hash TEXT PRIMARY KEY,
        node_id TEXT,
        name TEXT,
        is_online INTEGER NOT NULL DEFAULT 0,
        online_since INTEGER
      )
    ''');
  }

  // --- Messages ---

  /// Insert a new message.
  Future<void> insertMessage(RelayMessage message) async {
    final db = _db;
    if (db == null) return;
    await db.insert(
      'messages',
      message.toMap(),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
  }

  /// Update a message's delivery status.
  Future<void> updateMessageStatus(String id, MessageStatus status) async {
    final db = _db;
    if (db == null) return;
    await db.update(
      'messages',
      {'status': status.index},
      where: 'id = ?',
      whereArgs: [id],
    );
  }

  /// Get all messages for a conversation with a specific peer.
  /// Returns messages where the peer is either the sender or receiver.
  Future<List<RelayMessage>> getMessagesForPeer(String peerHash) async {
    final db = _db;
    if (db == null) return [];

    final results = await db.query(
      'messages',
      where: 'from_hash = ? OR to_hash = ?',
      whereArgs: [peerHash, peerHash],
      orderBy: 'timestamp ASC',
    );

    return results.map((m) => RelayMessage.fromMap(m)).toList();
  }

  /// Get the latest message per unique peer (for conversation list).
  Future<Map<String, RelayMessage>> getLatestMessages() async {
    final db = _db;
    if (db == null) return {};

    // Get all messages ordered by timestamp desc, then deduplicate by peer
    final results = await db.query(
      'messages',
      orderBy: 'timestamp DESC',
    );

    final latestByPeer = <String, RelayMessage>{};
    for (final row in results) {
      final msg = RelayMessage.fromMap(row);
      // Determine peer hash: if outgoing, peer is to_hash; if incoming, peer is from_hash
      final peerHash = msg.isOutgoing ? msg.toHash : msg.fromHash;
      if (!latestByPeer.containsKey(peerHash)) {
        latestByPeer[peerHash] = msg;
      }
    }

    return latestByPeer;
  }

  /// Count unread messages from a specific peer.
  Future<int> getUnreadCount(String peerHash) async {
    final db = _db;
    if (db == null) return 0;

    final result = await db.rawQuery(
      'SELECT COUNT(*) as count FROM messages WHERE from_hash = ? AND is_outgoing = 0',
      [peerHash],
    );

    return Sqflite.firstIntValue(result) ?? 0;
  }

  // --- Contacts ---

  /// Save or update a contact.
  Future<void> saveContact(Contact contact) async {
    final db = _db;
    if (db == null) return;
    await db.insert(
      'contacts',
      contact.toMap(),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
  }

  /// Get all contacts.
  Future<List<Contact>> getAllContacts() async {
    final db = _db;
    if (db == null) return [];

    final results = await db.query('contacts', orderBy: 'name ASC');
    return results.map((m) => Contact.fromMap(m)).toList();
  }

  /// Get a specific contact by user hash.
  Future<Contact?> getContact(String userHash) async {
    final db = _db;
    if (db == null) return null;

    final results = await db.query(
      'contacts',
      where: 'user_hash = ?',
      whereArgs: [userHash],
    );

    if (results.isEmpty) return null;
    return Contact.fromMap(results.first);
  }

  /// Close the database.
  Future<void> close() async {
    await _db?.close();
    _db = null;
  }
}
