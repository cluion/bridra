import 'dart:io';

final String repositoryFrameworkVersion = File(
  'VERSION',
).readAsStringSync().trim();
