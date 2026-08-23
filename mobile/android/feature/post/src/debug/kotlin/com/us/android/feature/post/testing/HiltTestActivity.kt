package com.us.android.feature.post.testing

import androidx.activity.ComponentActivity
import dagger.hilt.android.AndroidEntryPoint

/**
 * A Hilt-aware host for Compose journey tests — Slice C, C-CLB-2.
 *
 * `hiltViewModel()` resolves through the ACTIVITY's Hilt component, so a plain
 * `ComponentActivity` cannot host the real `composerScreen()` registration:
 * the lookup fails before anything under test runs. The Compose test manifest
 * supplies an unannotated `ComponentActivity`, which is why this exists.
 *
 * It lives in `src/debug` because that is the variant unit tests build against
 * and it must never reach a release binary.
 */
@AndroidEntryPoint
class HiltTestActivity : ComponentActivity()
