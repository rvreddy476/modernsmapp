// Root build file. Plugins are declared `apply false` here so that the
// versions resolve once from the catalog; individual modules apply the
// `us.*` convention plugins from build-logic instead of these directly.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
    alias(libs.plugins.ksp) apply false
    alias(libs.plugins.hilt) apply false
    alias(libs.plugins.detekt)
}

// Detekt runs over every module from the root, with detekt-formatting
// supplying the ktlint rule set. One tool instead of detekt + ktlint-gradle.
dependencies {
    detektPlugins(libs.detekt.formatting)
}

detekt {
    buildUponDefaultConfig = true
    allRules = false
    config.setFrom(files("$rootDir/config/detekt/detekt.yml"))
    source.setFrom(
        files(
            subprojects.map { "${it.projectDir}/src/main/kotlin" },
            subprojects.map { "${it.projectDir}/src/test/kotlin" },
        ),
    )
    parallel = true
}

tasks.withType<io.gitlab.arturbosch.detekt.Detekt>().configureEach {
    jvmTarget = libs.versions.javaTarget.get()
    reports {
        html.required.set(true)
        xml.required.set(true)
        sarif.required.set(true)
        txt.required.set(false)
        md.required.set(false)
    }
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}

/**
 * CI job 6 — enforces the module dependency rules from PHASE_0_1_PLAN §B
 * so they stay real rather than aspirational.
 *
 * Checks:
 *   1. :core:model is a plain Kotlin/JVM module with no Android plugin.
 *   2. No :core module depends on :app.
 *   3. No :feature module depends on another :feature module.
 *
 * Runs at configuration time against the project graph, so it costs nothing
 * at execution and cannot be forgotten.
 */
tasks.register("moduleGraphCheck") {
    group = "verification"
    description = "Asserts the module dependency rules in PHASE_0_1_PLAN §B."

    // Everything is resolved here, at configuration time, into plain
    // serializable values. The doLast block below closes over only a
    // List<String> and an Int — no Project, no script reference — which is
    // what keeps this task compatible with the configuration cache.
    val violations: List<String> = buildList {
        subprojects.find { it.path == ":core:model" }?.let { model ->
            listOf("com.android.library", "com.android.application").forEach { id ->
                if (model.pluginManager.hasPlugin(id)) {
                    add(":core:model must not apply '$id' — it is a pure Kotlin/JVM module.")
                }
            }
        }

        subprojects.forEach { sub ->
            val deps = sub.configurations
                .filter { it.name in setOf("implementation", "api") }
                .flatMap { config -> config.dependencies }
                .filterIsInstance<ProjectDependency>()
                .map { it.path }

            if (sub.path.startsWith(":core") && deps.contains(":app")) {
                add("${sub.path} must not depend on :app.")
            }
            if (sub.path.startsWith(":feature")) {
                deps.filter { it.startsWith(":feature") }.forEach { other ->
                    add(
                        "${sub.path} must not depend on $other — cross-feature " +
                            "navigation goes through route contracts in :app.",
                    )
                }
            }
        }
    }
    val moduleCount = subprojects.size

    doLast {
        if (violations.isNotEmpty()) {
            throw GradleException(
                "Module graph violations:\n" + violations.joinToString("\n") { "  - $it" },
            )
        }
        println("Module graph OK ($moduleCount modules checked).")
    }
}
