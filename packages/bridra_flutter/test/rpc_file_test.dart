import 'dart:convert';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('decodes a valid managed file reference', () {
    final content = utf8.encode('report');
    final reference = _reference(content, localPath: '/tmp/managed-report');

    expect(reference.name, 'report.txt');
    expect(reference.mediaType, 'text/plain');
    expect(reference.size, content.length);
    expect(reference.sha256, sha256.convert(content).toString());
    expect(reference.localPath, '/tmp/managed-report');
    expect(reference.isExpired, isFalse);
  });

  test('rejects malformed file references', () {
    final valid = _referenceJson(utf8.encode('report'));
    for (final invalid in [
      {...valid, 'id': 'short'},
      {...valid, 'name': '../secret'},
      {...valid, 'mediaType': 'text/plain\r\nx-unsafe: value'},
      {...valid, 'size': -1},
      {...valid, 'sha256': 'not-a-checksum'},
      {...valid, 'expiresAt': 'not-a-time'},
      {...valid, 'localPath': 42},
    ]) {
      expect(
        () => RpcFileReference.fromJson(invalid),
        throwsA(isA<BackendProtocolException>()),
      );
    }
  });

  test('verifies streamed size and SHA-256 without combining chunks', () async {
    final chunks = [utf8.encode('large '), utf8.encode('report')];
    final bytes = chunks.expand((chunk) => chunk).toList();
    final reference = _reference(bytes);

    final downloaded = await verifyRpcFileDownload(
      Stream.fromIterable(chunks),
      reference,
    ).toList();

    expect(downloaded, chunks);
  });

  test('rejects incomplete, oversized, and corrupted downloads', () async {
    final expected = utf8.encode('expected');
    final reference = _reference(expected);

    await expectLater(
      verifyRpcFileDownload(
        Stream.value(expected.sublist(0, expected.length - 1)),
        reference,
      ).toList(),
      throwsA(isA<BackendProtocolException>()),
    );
    await expectLater(
      verifyRpcFileDownload(Stream.value([...expected, 0]), reference).toList(),
      throwsA(isA<BackendProtocolException>()),
    );
    await expectLater(
      verifyRpcFileDownload(
        Stream.value(utf8.encode('corrupt!')),
        reference,
      ).toList(),
      throwsA(isA<BackendProtocolException>()),
    );
  });

  test('reports descriptor expiry using the local clock', () {
    final reference = RpcFileReference.fromJson({
      ..._referenceJson(utf8.encode('report')),
      'expiresAt': DateTime.now()
          .subtract(const Duration(minutes: 1))
          .toUtc()
          .toIso8601String(),
    });

    expect(reference.isExpired, isTrue);
  });
}

RpcFileReference _reference(List<int> content, {String? localPath}) {
  return RpcFileReference.fromJson(
    _referenceJson(content, localPath: localPath),
  );
}

Map<String, dynamic> _referenceJson(List<int> content, {String? localPath}) {
  return {
    'id': 'a' * 64,
    'name': 'report.txt',
    'mediaType': 'text/plain',
    'size': content.length,
    'sha256': sha256.convert(content).toString(),
    'expiresAt': DateTime.now()
        .add(const Duration(hours: 1))
        .toUtc()
        .toIso8601String(),
    'localPath': ?localPath,
  };
}
