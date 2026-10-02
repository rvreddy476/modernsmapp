plugins {
    id("us.android.library")
    id("us.android.compose")
}

android {
    namespace = "com.us.android.core.designsystem"
}

dependencies {
    implementation(projects.core.common)
    implementation(libs.coil.compose)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

// NoRawColourGuardTest reads the main Kotlin of EVERY module, so those files
// are this module's test inputs. Without this Gradle reports the test task up
// to date after a raw colour is added in another module, and the guard that
// exists to catch it never runs.
tasks.withType<Test>().configureEach {
    inputs.files(
        fileTree(rootDir) {
            include("**/src/main/**/*.kt")
            exclude("**/build/**", "**/.gradle/**")
        },
    ).withPropertyName("mainKotlinOfEveryModule").withPathSensitivity(PathSensitivity.RELATIVE)
}
