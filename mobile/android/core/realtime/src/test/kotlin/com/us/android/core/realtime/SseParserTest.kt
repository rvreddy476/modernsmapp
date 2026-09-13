package com.us.android.core.realtime

import com.google.common.truth.Truth.assertThat
import com.us.android.core.realtime.sse.SseFrame
import com.us.android.core.realtime.sse.SseParser
import org.junit.Test

class SseParserTest {

    @Test
    fun `an event with id, type and data dispatches on the blank line`() {
        val parser = SseParser()

        assertThat(parser.feed("id: 1717-0\nevent: food.order.1\ndata: {\"a\":1}\n")).isEmpty()
        val frames = parser.feed("\n")

        assertThat(frames).containsExactly(SseFrame(id = "1717-0", event = "food.order.1", data = "{\"a\":1}"))
        assertThat(parser.lastEventId).isEqualTo("1717-0")
    }

    @Test
    fun `multi-line data is joined with a line feed and loses only the final one`() {
        val frames = SseParser().feed("data: first\ndata: second\ndata:\ndata: fourth\n\n")

        assertThat(frames.single().data).isEqualTo("first\nsecond\n\nfourth")
    }

    @Test
    fun `comment lines, including the server keepalive, produce nothing`() {
        val parser = SseParser()

        assertThat(parser.feed(": keepalive\n\n: keepalive\n\n")).isEmpty()
        assertThat(parser.feed("data: x\n: a comment between data lines\ndata: y\n\n").single().data)
            .isEqualTo("x\ny")
    }

    @Test
    fun `a chunk may end mid-field, mid-value or between CR and LF`() {
        val stream = "id: 42\r\nevent: food.order.9\r\ndata: {\"status\":\"PICKED_UP\"}\r\n\r\n" +
            "data: second\r\rdata: third\n\n"
        val whole = SseParser().feed(stream)

        val parser = SseParser()
        val chunked = stream.map { parser.feed(it.toString()) }.flatten()

        assertThat(chunked).isEqualTo(whole)
        assertThat(chunked).containsExactly(
            SseFrame("42", "food.order.9", "{\"status\":\"PICKED_UP\"}"),
            SseFrame("42", "message", "second"),
            SseFrame("42", "message", "third"),
        ).inOrder()
    }

    @Test
    fun `one space after the colon is removed, and only one`() {
        val frames = SseParser().feed("data:nospace\n\ndata:  two spaces\n\ndata\n\n")

        assertThat(frames.map { it.data }).containsExactly("nospace", " two spaces", "").inOrder()
    }

    @Test
    fun `the id persists across events, and an id-only event still moves it`() {
        val parser = SseParser()

        val first = parser.feed("id: 1\ndata: a\n\ndata: b\n\n")
        assertThat(first.map { it.id }).containsExactly("1", "1").inOrder()

        assertThat(parser.feed("id: 2\n\n")).isEmpty()
        assertThat(parser.lastEventId).isEqualTo("2")

        parser.feed("id: bad" + Char(0) + "id\n\n")
        assertThat(parser.lastEventId).isEqualTo("2")

        parser.feed("id\n\n")
        assertThat(parser.lastEventId).isNull()
    }

    @Test
    fun `retry is honoured only when it is all digits`() {
        val parser = SseParser()

        parser.feed("retry: 5000\n\n")
        assertThat(parser.retryMillis).isEqualTo(5000L)

        parser.feed("retry: 5s\n\nretry: -1\n\n")
        assertThat(parser.retryMillis).isEqualTo(5000L)
    }

    @Test
    fun `an unnamed event is a message, unknown fields and a leading BOM are ignored`() {
        val frames = SseParser().feed(Char(0xFEFF) + "foo: bar\ndata: hello\n\n")

        assertThat(frames).containsExactly(SseFrame(id = null, event = "message", data = "hello"))
    }

    @Test
    fun `the server connected frame parses as notification-service writes it`() {
        val frames = SseParser().feed(
            "event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"food.order.1,food.order.2\",\"since\":\"\"}\n\n",
        )

        assertThat(frames.single().event).isEqualTo("connected")
    }
}
