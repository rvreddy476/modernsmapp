class MiniAppPermissionDefinition {
  final String key;
  final String title;
  final String description;

  const MiniAppPermissionDefinition({
    required this.key,
    required this.title,
    required this.description,
  });
}

const Map<String, MiniAppPermissionDefinition> miniAppPermissionDefinitions = {
  'clipboard.write': MiniAppPermissionDefinition(
    key: 'clipboard.write',
    title: 'Copy to clipboard',
    description: 'Lets the mini app copy text into your device clipboard.',
  ),
  'user.profile.read': MiniAppPermissionDefinition(
    key: 'user.profile.read',
    title: 'Read profile basics',
    description: 'Lets the mini app read your basic public AtPost profile.',
  ),
  'notifications.send': MiniAppPermissionDefinition(
    key: 'notifications.send',
    title: 'Show host alerts',
    description:
        'Lets the mini app show native AtPost alerts inside the mobile app.',
  ),
  'device.camera': MiniAppPermissionDefinition(
    key: 'device.camera',
    title: 'Capture camera image',
    description:
        'Lets the mini app open the device camera and receive a captured image.',
  ),
  'device.microphone': MiniAppPermissionDefinition(
    key: 'device.microphone',
    title: 'Request microphone access',
    description:
        'Lets the mini app check and request microphone access on this device.',
  ),
};

MiniAppPermissionDefinition miniAppPermissionFor(String key) {
  return miniAppPermissionDefinitions[key] ??
      MiniAppPermissionDefinition(
        key: key,
        title: key,
        description: 'Custom permission requested by this mini app.',
      );
}

List<String> normalizeMiniAppPermissions(List<String> permissions) {
  final seen = <String>{};
  final normalized = <String>[];
  for (final permission in permissions) {
    final value = permission.trim();
    if (value.isEmpty || seen.contains(value)) {
      continue;
    }
    seen.add(value);
    normalized.add(value);
  }
  return normalized;
}
