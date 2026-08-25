plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.telemetry"
    // Core library desugaring is enabled centrally for every module in
    // us.android.library — the OTel SDK needs it, and AGP requires the
    // consuming application to enable it too.
}

dependencies {
    api(projects.core.model)
    implementation(projects.core.common)

    api(platform(libs.opentelemetry.bom))
    api(libs.opentelemetry.api)
    implementation(libs.opentelemetry.sdk)
    implementation(libs.opentelemetry.exporter.otlp)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
}
