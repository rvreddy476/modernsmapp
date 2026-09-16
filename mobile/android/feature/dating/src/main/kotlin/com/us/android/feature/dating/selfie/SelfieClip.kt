package com.us.android.feature.dating.selfie

import java.io.File

/**
 * Where the blink-twice clip lives for the few seconds between the camera and
 * the uploader: one directory, one clip at a time.
 *
 * A biometric video has no business lingering, so the directory is emptied
 * before every recording and the uploader deletes the clip once it is sent.
 */
class SelfieClipStore(private val directory: File) {

    /** Empties the directory and names the next clip. Nothing is written yet. */
    fun next(stamp: Long): File {
        clear()
        directory.mkdirs()
        return File(directory, "selfie-$stamp.mp4")
    }

    /** Deletes every clip left behind. Harmless when the directory never existed. */
    fun clear() {
        directory.listFiles()?.forEach { it.delete() }
    }
}

/** What the selfie recorder is doing. */
internal enum class SelfiePhase { Idle, Starting, Recording, Stopping }

/**
 * One clip at a time.
 *
 * The camera's callbacks arrive on the main thread and can arrive late — after
 * the limit stopped the clip, after a cancel, after the screen has gone — so
 * every transition is decided here instead of being guessed at the call site.
 * No camera types, so it is tested on the JVM.
 */
internal class SelfieRecordingState {

    var phase: SelfiePhase = SelfiePhase.Idle
        private set

    /** The in-flight clip was cancelled: its result is thrown away, not delivered. */
    var abandoned: Boolean = false
        private set

    val isBusy: Boolean get() = phase != SelfiePhase.Idle

    /** A recording may begin only when nothing else is running. */
    fun beginRequested(): Boolean {
        if (phase != SelfiePhase.Idle) return false
        phase = SelfiePhase.Starting
        abandoned = false
        return true
    }

    fun started() {
        if (phase == SelfiePhase.Starting) phase = SelfiePhase.Recording
    }

    /** The limit ran out, or the step asked to stop. False when there is nothing to stop. */
    fun stopRequested(): Boolean {
        if (phase == SelfiePhase.Idle || phase == SelfiePhase.Stopping) return false
        phase = SelfiePhase.Stopping
        return true
    }

    /**
     * The screen is leaving, the permission went away, or the person backed out.
     * False when nothing was running — a finished clip already belongs to the
     * uploader and must not be touched.
     */
    fun cancelled(): Boolean {
        if (phase == SelfiePhase.Idle) return false
        abandoned = true
        phase = SelfiePhase.Stopping
        return true
    }

    /** The recorder finished. True when the result should still reach the caller. */
    fun finished(): Boolean {
        val deliver = phase != SelfiePhase.Idle && !abandoned
        phase = SelfiePhase.Idle
        return deliver
    }
}

/** Whether a finished recording produced something worth uploading. Pure. */
internal object SelfieClipOutcome {

    /**
     * [fatal] is the camera's verdict — the duration and size caps are how the
     * clip is kept inside the server's limits, so they are not failures. A clip
     * with no bytes is useless whatever the camera said.
     */
    fun usable(fatal: Boolean, file: File): Boolean = !fatal && file.isFile && file.length() > 0L
}
