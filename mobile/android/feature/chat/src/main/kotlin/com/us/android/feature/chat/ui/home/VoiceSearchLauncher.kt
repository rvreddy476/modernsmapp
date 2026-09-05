package com.us.android.feature.chat.ui.home

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat

/** What the mic can do right now: start listening. */
internal fun interface VoiceSearchLauncher {
    fun start()
}

/**
 * The search pill's mic (founder, 2026-09-05: "a search bar with voice
 * search"). Android's own recogniser through [RecognizerIntent] — free-form
 * speech, one hypothesis list back — behind the RECORD_AUDIO runtime flow:
 *
 *  1. no recogniser on the device → [onUnavailable], nothing launched;
 *  2. permission not yet granted → ask; a grant starts listening at once,
 *     a denial explains itself through [onUnavailable];
 *  3. a result fills the field through [onResult]; a cancelled dialog is
 *     silent — the user closed it, there is nothing to say.
 *
 * The recogniser's own dialog is used rather than a bespoke listener: it
 * handles the audio focus, the partial results and the timeouts, and it is
 * the surface the user already knows from the keyboard's mic.
 */
@Composable
internal fun rememberVoiceSearch(
    onResult: (List<String>) -> Unit,
    onUnavailable: (String) -> Unit,
): VoiceSearchLauncher {
    val context = LocalContext.current
    val latestResult = rememberUpdatedState(onResult)
    val latestUnavailable = rememberUpdatedState(onUnavailable)

    val recogniser = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode != Activity.RESULT_OK) return@rememberLauncherForActivityResult
        val spoken = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS).orEmpty()
        latestResult.value(spoken)
    }

    val launchRecogniser = remember(context) {
        {
            if (SpeechRecognizer.isRecognitionAvailable(context)) {
                val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                    putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                    putExtra(RecognizerIntent.EXTRA_PROMPT, "Search messages")
                    putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, MAX_HYPOTHESES)
                }
                runCatching { recogniser.launch(intent) }
                    .onFailure { latestUnavailable.value(RECOGNISER_MISSING) }
            } else {
                latestUnavailable.value(RECOGNISER_MISSING)
            }
        }
    }

    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) launchRecogniser() else latestUnavailable.value(PERMISSION_DENIED)
    }

    return remember(context) {
        VoiceSearchLauncher {
            val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) ==
                PackageManager.PERMISSION_GRANTED
            if (granted) launchRecogniser() else permission.launch(Manifest.permission.RECORD_AUDIO)
        }
    }
}

private const val MAX_HYPOTHESES = 3
private const val RECOGNISER_MISSING = "Voice search isn't available on this device."
private const val PERMISSION_DENIED = "Allow microphone access to search by voice."
