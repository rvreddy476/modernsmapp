package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.network.AddFavouriteRequest
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The favourites endpoints on the wire.
 *
 * These exist because the add used to be declared as `POST /favourites/{id}`,
 * a route commerce-service never registered, so every heart tap was a 404
 * and the optimistic UI flipped it straight back. The server's contract
 * (handler_storefront.go) is `POST /v1/commerce/favourites` with
 * `{"product_id":"..."}`; the list and the remove are path-shaped as before.
 * A test on the Retrofit declaration is the only thing that catches a path
 * typo before a device does.
 */
class CommerceApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: CommerceApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CommerceApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    @Test
    fun `add favourite posts the collection path with the product id in the body`() {
        enqueue("""{"data":{"product_id":"p-1","is_favourite":true}}""")

        val answer = runBlocking { api.addFavourite(AddFavouriteRequest(productId = "p-1")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites")
        assertThat(request.body!!.utf8()).isEqualTo("""{"product_id":"p-1"}""")

        // The server echoes what it now holds; decode it rather than Unit so
        // a future "not saved" answer can be honoured without a wire change.
        val dto = answer.body()!!.data!!
        assertThat(dto.productId).isEqualTo("p-1")
        assertThat(dto.isFavourite).isTrue()
    }

    @Test
    fun `remove favourite deletes the item path`() {
        enqueue("""{"data":{"product_id":"p-1","is_favourite":false}}""")

        runBlocking { api.removeFavourite("p-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites/p-1")
    }

    @Test
    fun `the favourites list is a plain get on the collection`() {
        enqueue("""{"data":{"items":[],"next_cursor":null}}""")

        runBlocking { api.favourites() }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites")
    }
}
