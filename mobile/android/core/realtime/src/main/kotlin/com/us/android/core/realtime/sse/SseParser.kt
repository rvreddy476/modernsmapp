package com.us.android.core.realtime.sse

/**
 * One dispatched server-sent event.
 *
 * [id] is the stream's last event id AT DISPATCH, which is what the WHATWG
 * interpretation rules make it: an `id:` persists across later events that
 * carry none. [event] is `message` when the frame named no type.
 */
data class SseFrame(
    val id: String?,
    val event: String,
    val data: String,
)

/**
 * A pure, incremental `text/event-stream` parser.
 *
 * Follows the WHATWG "event stream interpretation" rules for the parts this
 * platform uses:
 *  - lines end in LF, CRLF or a lone CR, and a chunk may end mid-line or
 *    between the CR and LF of one line ending;
 *  - `field: value` with ONE optional space after the colon removed;
 *  - `data:` lines accumulate and are joined with `\n`;
 *  - a line starting with `:` is a comment (notification-service writes
 *    `: keepalive` every 25 s) and changes nothing;
 *  - `id:` sets the last event id unless it contains NUL; `retry:` is honoured
 *    only when it is all ASCII digits;
 *  - a blank line dispatches, and an event with an empty data buffer is not
 *    dispatched (but its `id:` still takes effect).
 *
 * No I/O and no Android: fed strings, returns frames. One instance per
 * connection.
 */
class SseParser {

    /** The last event id seen on this stream, or null when none (or reset). */
    var lastEventId: String? = null
        private set

    /** The server's `retry:` reconnection hint in milliseconds, if it sent one. */
    var retryMillis: Long? = null
        private set

    private val line = StringBuilder()
    private val data = StringBuilder()
    private var eventType: String? = null
    private var idBuffer: String? = null
    private var previousWasCr = false
    private var atStreamStart = true

    /** Feeds the next decoded chunk and returns every event it completed. */
    fun feed(chunk: String): List<SseFrame> {
        val out = mutableListOf<SseFrame>()
        for (ch in chunk) {
            if (atStreamStart) {
                atStreamStart = false
                if (ch == BOM) continue
            }
            when {
                ch == LF && previousWasCr -> previousWasCr = false
                ch == CR -> {
                    previousWasCr = true
                    endLine(out)
                }
                ch == LF -> endLine(out)
                else -> {
                    previousWasCr = false
                    line.append(ch)
                }
            }
        }
        return out
    }

    private fun endLine(out: MutableList<SseFrame>) {
        val text = line.toString()
        line.setLength(0)
        when {
            text.isEmpty() -> dispatch(out)
            text[0] == ':' -> Unit
            else -> {
                val colon = text.indexOf(':')
                if (colon < 0) {
                    field(text, "")
                } else {
                    field(text.substring(0, colon), text.substring(colon + 1).removePrefix(" "))
                }
            }
        }
    }

    private fun field(name: String, value: String) {
        when (name) {
            "event" -> eventType = value
            "data" -> data.append(value).append(LF)
            "id" -> if (NUL !in value) idBuffer = value
            "retry" -> if (value.isNotEmpty() && value.all { it in '0'..'9' }) {
                value.toLongOrNull()?.let { retryMillis = it }
            }
            else -> Unit
        }
    }

    private fun dispatch(out: MutableList<SseFrame>) {
        if (idBuffer != null) lastEventId = idBuffer?.takeIf { it.isNotEmpty() }
        if (data.isEmpty()) {
            eventType = null
            return
        }
        val payload = data.toString().removeSuffix(LF.toString())
        data.setLength(0)
        out += SseFrame(
            id = lastEventId,
            event = eventType?.takeIf { it.isNotEmpty() } ?: DEFAULT_EVENT,
            data = payload,
        )
        eventType = null
    }

    private companion object {
        const val DEFAULT_EVENT = "message"
        const val LF = '\n'
        const val CR = '\r'
        val NUL = Char(0)
        val BOM = Char(0xFEFF)
    }
}
