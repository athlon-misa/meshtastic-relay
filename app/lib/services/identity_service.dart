import 'dart:typed_data';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:convert/convert.dart';
import '../models/identity.dart';

/// Manages the user's Ed25519 cryptographic identity.
///
/// On first launch, generates a new keypair. On subsequent launches,
/// loads the stored keypair from secure storage.
class IdentityService {
  static const _storageKeyPrivate = 'relay_identity_private_key';
  static const _storageKeyHash = 'relay_identity_hash';

  final FlutterSecureStorage _storage;
  RelayIdentity? _identity;

  IdentityService({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  /// Current identity, or null if not yet initialized.
  RelayIdentity? get identity => _identity;

  /// Whether an identity has been loaded or generated.
  bool get hasIdentity => _identity != null;

  /// The user's hex-encoded identity hash, or null if not initialized.
  String? get userHash => _identity?.userHash;

  /// Initialize the identity service. Loads existing identity from secure
  /// storage, or returns null if none exists (caller should then generate
  /// or import).
  Future<RelayIdentity?> initialize() async {
    final storedKey = await _storage.read(key: _storageKeyPrivate);
    if (storedKey != null) {
      try {
        final keyBytes = Uint8List.fromList(hex.decode(storedKey));
        _identity = await RelayIdentity.fromPrivateKeyBytes(keyBytes);
        return _identity;
      } catch (e) {
        // Corrupted key -- clear it
        await _storage.delete(key: _storageKeyPrivate);
        await _storage.delete(key: _storageKeyHash);
        return null;
      }
    }
    return null;
  }

  /// Generate a new identity and store it securely.
  Future<RelayIdentity> generateNew() async {
    _identity = await RelayIdentity.generate();
    await _persistIdentity();
    return _identity!;
  }

  /// Import an identity from raw private key bytes (64-byte Ed25519 key
  /// from the Go backend, or 32-byte seed).
  Future<RelayIdentity> importFromBytes(Uint8List privateKeyBytes) async {
    _identity = await RelayIdentity.fromPrivateKeyBytes(privateKeyBytes);
    await _persistIdentity();
    return _identity!;
  }

  /// Import an identity from a hex-encoded private key string.
  Future<RelayIdentity> importFromHex(String hexKey) async {
    final bytes = Uint8List.fromList(hex.decode(hexKey));
    return importFromBytes(bytes);
  }

  /// Export the current identity's private key as hex for backup.
  Future<String?> exportAsHex() async {
    if (_identity == null) return null;
    final bytes = await _identity!.exportPrivateKey();
    return hex.encode(bytes);
  }

  /// Sign a message with the current identity.
  Future<Uint8List> sign(Uint8List message) async {
    if (_identity == null) {
      throw StateError('No identity loaded');
    }
    return _identity!.sign(message);
  }

  /// Delete the stored identity (dangerous -- identity cannot be recovered).
  Future<void> deleteIdentity() async {
    await _storage.delete(key: _storageKeyPrivate);
    await _storage.delete(key: _storageKeyHash);
    _identity = null;
  }

  Future<void> _persistIdentity() async {
    if (_identity == null) return;
    final keyBytes = await _identity!.exportPrivateKey();
    await _storage.write(
      key: _storageKeyPrivate,
      value: hex.encode(keyBytes),
    );
    await _storage.write(
      key: _storageKeyHash,
      value: _identity!.userHash,
    );
  }
}
