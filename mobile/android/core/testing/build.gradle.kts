plugins {
    id("us.android.library")
}

android {
    namespace = "com.us.android.core.testing"
}

// Test infrastructure shared by every module. Exists from Phase 0 rather
// than Phase 1 so fixtures and rules are never duplicated across modules
// (PHASE_0_1_PLAN §A, modification M4).
dependencies {
    api(projects.core.model)
    api(projects.core.common)

    api(libs.junit)
    api(libs.truth)
    api(libs.turbine)
    api(libs.mockk)
    api(libs.kotlinx.coroutines.test)
    api(libs.robolectric)
    api(libs.androidx.test.ext.junit)
}
