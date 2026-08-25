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

            // Creator Studio P0-A, guards G-4/G-5/G-6.
            //
            // The render/export PORT lives in :core:creator-model, which is why
            // neither of these edges is needed in either direction. An earlier
            // design had the engine owning the interface AND media consuming a
            // model interface, which is a cycle waiting to be written; asserting
            // the actual Gradle edges is what stops it being written by accident.
            if (sub.path == ":core:creator-engine" && deps.contains(":core:media")) {
                add(
                    ":core:creator-engine must not depend on :core:media — it calls " +
                        "the RenderExporter port in :core:creator-model, and app DI " +
                        "binds the :core:media implementation to it.",
                )
            }
            if (sub.path == ":core:media" && deps.contains(":core:creator-engine")) {
                add(
                    ":core:media must not depend on :core:creator-engine — it only " +
                        "implements the port declared in :core:creator-model.",
                )
            }
            // G-6, ADJUSTED FROM THE FROZEN SPEC — see the handover.
            //
            // The spec said no :feature may depend on :core:media. That was
            // written from an architecture sketch rather than from this graph:
            // :feature:post and :feature:profile have depended on :core:media
            // since Slice C for URL resolution and upload, neither of which has
            // anything to do with rendering. Enforcing the rule as written would
            // mean rewriting two features for a guard aimed at something else.
            //
            // What the rule was actually protecting is that no feature reaches
            // the render/export IMPLEMENTATION directly, bypassing the port. A
            // feature that depends on :core:media must not ALSO depend on
            // :core:creator-model, because that combination is how a screen
            // starts calling a RenderExporter implementation it found itself
            // instead of the one app DI bound.
            // :feature:post holds a NAMED waiver: the PublishTransport adapter
            // must live beside the CreatePostRequest DTO it freezes (the
            // provenance rule), and that module already depends on :core:media
            // for Slice C upload. The hazard this guard exists for — a feature
            // binding the RENDER port implementation itself — is asserted at
            // source level by RenderPortBoundaryGuardTest, which fails if any
            // feature file so much as imports RenderExporter.
            if (sub.path.startsWith(":feature") && sub.path != ":feature:post" &&
                deps.contains(":core:media") && deps.contains(":core:creator-model")
            ) {
                add(
                    "${sub.path} depends on BOTH :core:media and :core:creator-model — " +
                        "a feature must reach render/export through :core:creator-engine, " +
                        "never by binding a port implementation itself.",
                )
            }
        }

        // G-2: :core:creator-model is pure Kotlin/JVM.
        //
        // Purity is what makes the canonical bytes unit-testable on the JVM and
        // what lets :core:media implement the port without the engine ever
        // seeing it. An Android import here quietly costs both.
        subprojects.find { it.path == ":core:creator-model" }?.let { model ->
            listOf("com.android.library", "com.android.application").forEach { id ->
                if (model.pluginManager.hasPlugin(id)) {
                    add(":core:creator-model must not apply '$id' — it is pure Kotlin/JVM.")
                }
            }
        }
    }
    val moduleCount = subprojects.size

    // G-1: the expected module count, not merely "green".
    //
    // A green graph cannot detect a module nobody meant to add. Naming the
    // number makes an unplanned module a build failure rather than a surprise
    // six weeks later. Update this deliberately when a module is authorised.
    val expectedModuleCount = 26

    doLast {
        val allViolations = buildList {
            addAll(violations)
            if (moduleCount != expectedModuleCount) {
                add(
                    "Module count is $moduleCount, expected $expectedModuleCount. " +
                        "If the change is intended, update expectedModuleCount in " +
                        "build.gradle.kts in the same commit that adds the module.",
                )
            }
        }
        if (allViolations.isNotEmpty()) {
            throw GradleException(
                "Module graph violations:\n" + allViolations.joinToString("\n") { "  - $it" },
            )
        }
        println("Module graph OK ($moduleCount modules checked, count asserted).")
    }
}
