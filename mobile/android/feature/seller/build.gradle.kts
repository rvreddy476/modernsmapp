plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}
android {
    namespace = "com.us.android.feature.seller"
}
kotlin.sourceSets.getByName("test").kotlin.srcDir("../../test-fixtures/commerce")
dependencies {
    implementation(libs.coil.compose)
    implementation(projects.core.commerce)
    implementation(projects.core.commerceUi)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.auth)
    implementation(projects.core.media)
    implementation(projects.core.network)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    testImplementation(projects.core.testing)
    testImplementation(libs.kotlinx.coroutines.test)
}
