import 'dart:convert';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';
import 'package:convert/convert.dart';

/// Represents a cryptographic identity with an Ed25519 keypair.
///
/// Matches the Go backend: identity hash = SHA-256(public_key) truncated to
/// 16 bytes, hex-encoded.
class RelayIdentity {
  final SimpleKeyPair _keyPair;
  final Uint8List publicKeyBytes;
  final String userHash;

  RelayIdentity._({
    required SimpleKeyPair keyPair,
    required this.publicKeyBytes,
    required this.userHash,
  }) : _keyPair = keyPair;

  /// Generate a new random Ed25519 identity.
  static Future<RelayIdentity> generate() async {
    final algorithm = Ed25519();
    final keyPair = await algorithm.newKeyPair();
    final simpleKeyPair = await keyPair.extract();
    final publicKey = await simpleKeyPair.extractPublicKey();
    final pubBytes = Uint8List.fromList(publicKey.bytes);
    final hash = await _computeHash(pubBytes);
    return RelayIdentity._(
      keyPair: simpleKeyPair,
      publicKeyBytes: pubBytes,
      userHash: hash,
    );
  }

  /// Restore an identity from a stored private key (64-byte Ed25519 seed+pub
  /// or 32-byte seed).
  static Future<RelayIdentity> fromPrivateKeyBytes(Uint8List privateKeyBytes) async {
    final algorithm = Ed25519();
    SimpleKeyPair keyPair;

    if (privateKeyBytes.length == 64) {
      // Full Ed25519 private key: first 32 bytes are seed, last 32 are public key
      final seed = privateKeyBytes.sublist(0, 32);
      final pubBytes = privateKeyBytes.sublist(32, 64);
      keyPair = SimpleKeyPairData(
        seed,
        publicKey: SimplePublicKey(pubBytes, type: KeyPairType.ed25519),
        type: KeyPairType.ed25519,
      );
    } else if (privateKeyBytes.length == 32) {
      // 32-byte seed only -- derive keypair
      final newPair = await algorithm.newKeyPairFromSeed(privateKeyBytes);
      keyPair = await newPair.extract();
    } else {
      throw ArgumentError(
        'Invalid private key size: ${privateKeyBytes.length} (expected 32 or 64)',
      );
    }

    final publicKey = await keyPair.extractPublicKey();
    final pubBytes = Uint8List.fromList(publicKey.bytes);
    final hash = await _computeHash(pubBytes);
    return RelayIdentity._(
      keyPair: keyPair,
      publicKeyBytes: pubBytes,
      userHash: hash,
    );
  }

  /// Export the full private key bytes (64 bytes: seed + public key) for storage.
  Future<Uint8List> exportPrivateKey() async {
    final privData = await _keyPair.extractPrivateKeyBytes();
    final pubKey = await _keyPair.extractPublicKey();
    // Combine seed + public key into Go-compatible 64-byte format
    final result = Uint8List(64);
    result.setRange(0, 32, privData);
    result.setRange(32, 64, pubKey.bytes);
    return result;
  }

  /// Sign a message with the Ed25519 private key.
  Future<Uint8List> sign(Uint8List message) async {
    final algorithm = Ed25519();
    final signature = await algorithm.sign(message, keyPair: _keyPair);
    return Uint8List.fromList(signature.bytes);
  }

  /// Compute SHA-256(publicKey) truncated to 16 bytes, hex-encoded.
  /// Matches Go backend: `computeHash` in identity/identity.go.
  static Future<String> _computeHash(Uint8List publicKey) async {
    final sha256 = Sha256();
    final hashResult = await sha256.hash(publicKey);
    final truncated = hashResult.bytes.sublist(0, 16);
    return hex.encode(truncated);
  }

  /// Get the hex-encoded public key.
  String get publicKeyHex => hex.encode(publicKeyBytes);

  /// Short hash for display (first 12 hex chars = 6 bytes).
  String get shortHash =>
      userHash.length > 12 ? userHash.substring(0, 12) : userHash;

  @override
  String toString() => 'RelayIdentity($shortHash)';
}
