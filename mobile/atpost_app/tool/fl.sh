#!/usr/bin/env bash
#
# fl.sh — `flutter` wrapper that hides Flutter's known-harmless "Built-in Kotlin
# / plugins apply Kotlin Gradle Plugin (KGP)" deprecation warning.
#
# Why this exists:
#   Flutter's Gradle plugin scans every third-party plugin's build.gradle and,
#   for any that still apply KGP, prints a multi-line WARNING at ERROR level on
#   EVERY build. As of this Flutter version there is NO config flag to disable it
#   (it is called unconditionally in FlutterPluginUtils.detectApplyingKotlinGradlePlugin).
#
#   We already upgraded every plugin we safely can (8 -> 5 offenders). The
#   remaining offenders — device_info_plus, flutter_webrtc, livekit_client,
#   package_info_plus, wakelock_plus — are anchored by livekit_client, which has
#   no Built-in-Kotlin release yet. Forcing them either breaks `pub` resolution
#   or forces a flutter_secure_storage major bump (it stores our auth tokens).
#
#   The warning is non-fatal: the app builds, installs and runs. This wrapper
#   only filters DISPLAY — build behaviour is identical. When the upstream
#   plugins migrate, run `flutter pub upgrade` and delete this script.
#
# Usage: ./tool/fl.sh build apk --debug
#        ./tool/fl.sh run -d <device>
#   (For interactive `flutter run` hot-reload keys, prefer plain `flutter run`;
#    piping disables the interactive TTY. Use this wrapper mainly for builds.)
#
set -o pipefail

flutter "$@" 2>&1 | grep --line-buffered -vE \
  -e 'plugins that apply Kotlin Gradle Plugin \(KGP\)' \
  -e 'Future versions of Flutter will fail to build if your app uses plugins that apply KGP' \
  -e 'Please check the changelogs of these plugins and upgrade to a version that supports Built-in Kotlin' \
  -e 'report the issue to the plugin\. If necessary' \
  -e 'an issue against a plugin:' \
  -e 'If you are a plugin author, please migrate your plugin to Built-in Kotlin' \
  -e 'applies the Kotlin Gradle Plugin, which will cause build failures' \
  -e 'Please migrate your app to Built-in Kotlin using this guide' \
  -e 'WARNING: Your Android app project:' \
  -e 'WARNING: Your app uses the following' \
  -e 'migrate-to-built-in-kotlin'
