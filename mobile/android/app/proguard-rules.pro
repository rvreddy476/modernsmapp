# Phase 0: no custom rules yet. R8 full mode is on by default in AGP 8.x.
# Rules for kotlinx.serialization / Retrofit arrive with Phase 1.

# -- Banuba Video Editor SDK (2026-09-05) --------------------------------
# Rules from the Banuba integration sample at 1.54.1. The effects packages
# are reached reflectively by the native layer; the dontwarn lines silence
# the R8 missing-class report for optional transitive dependencies the SDK
# never loads at runtime.
-keep class com.banuba.sdk.core.effects.** { *; }
-keep class com.banuba.sdk.effects.ve.speed.** { *; }
-keep class kotlinx.android.extensions.** { *; }
-dontwarn kotlinx.android.extensions.LayoutContainer
-dontwarn kotlinx.parcelize.Parcelize
-dontwarn org.bouncycastle.jsse.BCSSLParameters
-dontwarn org.bouncycastle.jsse.BCSSLSocket
-dontwarn org.bouncycastle.jsse.provider.BouncyCastleJsseProvider
-dontwarn org.conscrypt.Conscrypt$Version
-dontwarn org.conscrypt.Conscrypt
-dontwarn org.conscrypt.ConscryptHostnameVerifier
-dontwarn org.openjsse.javax.net.ssl.SSLParameters
-dontwarn org.openjsse.javax.net.ssl.SSLSocket
-dontwarn org.openjsse.net.ssl.OpenJSSE
# ar-cloud is deliberately not a dependency (paid, unneeded): camera-ui-sdk
# and ve-flow-sdk reference it from code paths that only run with it present.
-dontwarn com.banuba.sdk.arcloud.**

# -- Optional hooks of libraries already in the graph ---------------------
# Reported by R8 as missing classes on the first release build that ran
# after the Banuba integration (2026-09-05); none is Banuba's. The OTel SDK
# and OTLP exporter reference the incubator API and the autoconfigure SPI,
# neither of which this app ships (telemetry is wired explicitly); LiveKit
# references an OkHttp 4 internal that OkHttp 5 no longer has.
-dontwarn io.opentelemetry.api.incubator.**
-dontwarn io.opentelemetry.sdk.autoconfigure.spi.**
-dontwarn okhttp3.internal.Util
