package com.us.android.feature.settings.content

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.KeywordFiltersApi
import com.us.android.core.profile.data.KeywordFiltersCache
import com.us.android.core.profile.data.KeywordFiltersRepository
import com.us.android.core.profile.data.dto.KeywordFiltersDto
import com.us.android.core.profile.data.dto.UpdateKeywordFiltersRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class ContentPreferencesViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : KeywordFiltersApi {
        var keywords: List<String> = emptyList()

        override suspend fun keywordFilters() = ApiEnvelope(KeywordFiltersDto(keywords))

        override suspend fun updateKeywordFilters(body: UpdateKeywordFiltersRequest): ApiEnvelope<KeywordFiltersDto> {
            keywords = body.keywords
            return ApiEnvelope(KeywordFiltersDto(keywords))
        }
    }

    private class NoOpCache : KeywordFiltersCache {
        override suspend fun read(): List<String> = emptyList()
        override suspend fun write(keywords: List<String>) = Unit
    }

    private fun buildViewModel(api: FakeApi = FakeApi()) =
        ContentPreferencesViewModel(KeywordFiltersRepository(api, NoOpCache(), ErrorMapper(json))) to api

    private fun ContentPreferencesViewModel.editing() = state.value as ContentPreferencesUiState.Editing

    // ── KeywordValidation ────────────────────────────────────────────────

    @Test
    fun `normalize lower-cases, trims and strips a leading hash`() {
        assertThat(KeywordValidation.normalize("  #Spoilers  ")).isEqualTo("spoilers")
    }

    @Test
    fun `an empty keyword is rejected`() {
        assertThat(KeywordValidation.validate("   ", emptyList())).isNotNull()
    }

    @Test
    fun `a keyword over 40 characters is rejected`() {
        val tooLong = "a".repeat(41)
        assertThat(KeywordValidation.validate(tooLong, emptyList())).isNotNull()
        assertThat(KeywordValidation.validate("a".repeat(40), emptyList())).isNull()
    }

    @Test
    fun `a duplicate keyword is rejected case-insensitively`() {
        assertThat(KeywordValidation.validate("Spoilers", listOf("spoilers"))).isNotNull()
    }

    @Test
    fun `the 51st keyword is rejected`() {
        val existing = (1..50).map { "kw$it" }
        assertThat(KeywordValidation.validate("kw51", existing)).isNotNull()
    }

    // ── ViewModel ────────────────────────────────────────────────────────

    @Test
    fun `adding a valid keyword normalises it and saves`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setDraft("#Spoilers")
        viewModel.addKeyword()

        assertThat(api.keywords).containsExactly("spoilers")
        assertThat(viewModel.editing().keywords).containsExactly("spoilers")
        assertThat(viewModel.editing().draft).isEmpty()
    }

    @Test
    fun `adding an invalid keyword sets a field error and does not save`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setDraft("   ")
        viewModel.addKeyword()

        assertThat(viewModel.editing().draftError).isNotNull()
        assertThat(api.keywords).isEmpty()
    }

    @Test
    fun `removing a keyword saves the list without it`() = runTest {
        val api = FakeApi().apply { keywords = listOf("spoilers", "politics") }
        val (viewModel, _) = buildViewModel(api)

        viewModel.removeKeyword("spoilers")

        assertThat(viewModel.editing().keywords).containsExactly("politics")
        assertThat(api.keywords).containsExactly("politics")
    }
}
