import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bridra_flutter/bridra_flutter.dart';
import 'package:bridra_flutter/src/single_instance/desktop_single_instance_io.dart'
    as single_instance_io;

Future<void> main(List<String> arguments) async {
  if (arguments.length < 3) {
    stderr.writeln(
      'Usage: single_instance_helper.dart '
      '<runtime-directory> <application-id> <working-directory> [arguments...]',
    );
    exitCode = 64;
    return;
  }

  final session = await DesktopSingleInstance.acquire(
    runtimeDirectory: arguments[0],
    applicationId: arguments[1],
    workingDirectory: arguments[2],
    arguments: arguments.skip(3),
  );
  stdout.writeln(
    jsonEncode({
      'role': session.isPrimary ? 'primary' : 'secondary',
      'pid': pid,
    }),
  );
  final readyFile =
      Platform.environment['BRIDRA_SINGLE_INSTANCE_HELPER_READY_FILE'];
  if (!session.isPrimary &&
      Platform.environment['BRIDRA_SINGLE_INSTANCE_HELPER_MODE'] ==
          'terminate') {
    // A live event source catches the false success of merely returning from main.
    Timer.periodic(const Duration(minutes: 1), (_) {});
    await stdout.flush();
    if (Platform.isMacOS) {
      await single_instance_io.terminateSecondary(
        session,
        requiresNativeTermination: false,
      );
    } else {
      await DesktopSingleInstance.terminateSecondary(session);
    }
    stdout.writeln('termination returned without exiting');
    return;
  }
  if (session.isPrimary && readyFile != null && readyFile.isNotEmpty) {
    await File(readyFile).writeAsString('$pid', flush: true);
  }

  if (session.isPrimary &&
      Platform.environment['BRIDRA_SINGLE_INSTANCE_HELPER_MODE'] == 'abandon') {
    await stdout.flush();
    exit(0);
  }
  if (session.isPrimary &&
      Platform.environment['BRIDRA_SINGLE_INSTANCE_HELPER_MODE'] ==
          'coordinate') {
    final activation = await session.activations.first.timeout(
      const Duration(seconds: 10),
    );
    stdout.writeln(
      jsonEncode({
        'receivedArguments': activation.arguments,
        'receivedWorkingDirectory': activation.workingDirectory,
      }),
    );
  }
  await session.close();
}
