package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.DataSaverStore
import com.us.android.core.profile.data.AccountDataSource
import com.us.android.core.profile.data.VerificationKind
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.profile.navigation.SettingsSections
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.OutputStream

class AccountDataViewModelsTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeStore : DataSaverStore {
        val flow = MutableStateFlow(false)
        override val enabled = flow
        override suspend fun setEnabled(enabled: Boolean) {
            flow.value = enabled
        }
    }

    private class FakeSource : AccountDataSource {
        var export: AppResult<Long> = AppResult.Success(2)
        var verification: AppResult<String> = AppResult.Success("pending")
        val asked = mutableListOf<String>()

        override suspend fun exportTo(output: OutputStream): AppResult<Long> {
            if (export is AppResult.Success) output.write("{}".toByteArray())
            return export
        }

        override suspend fun requestVerification(kind: VerificationKind, note: String): AppResult<String> {
            asked += "${kind.wire}:$note"
            return verification
        }
    }


    @Test
    fun `the data saver switch is the stored value, and a flip stores it`() = runTest {
        val store = FakeStore()
        val vm = AccountDataViewModel(store, FakeSource(), { error("not used") }, kotlinx.coroutines.Dispatchers.Main)

        vm.setDataSaver(true)
        advanceUntilIdle()

        assertThat(store.flow.first()).isTrue()
    }

    @Test
    fun `the export is written into the picked file and says so`() = runTest {
        val written = ByteArrayOutputStream()
        val vm = AccountDataViewModel(FakeStore(), FakeSource(), { error("not used") }, kotlinx.coroutines.Dispatchers.Main)

        vm.exportWith { written }
        advanceUntilIdle()

        assertThat(written.toString()).isEqualTo("{}")
        assertThat(vm.export.value.message).isEqualTo(AccountDataViewModel.EXPORT_SAVED)
        assertThat(vm.export.value.failed).isFalse()
    }

    @Test
    fun `backing out of Save to does nothing`() = runTest {
        var opened = 0
        val vm = AccountDataViewModel(FakeStore(), FakeSource(), { opened++; ByteArrayOutputStream() }, kotlinx.coroutines.Dispatchers.Main)

        vm.exportTo(null)
        advanceUntilIdle()

        assertThat(opened).isEqualTo(0)
        assertThat(vm.export.value).isEqualTo(DataExportUiState())
    }

    @Test
    fun `a failed download or an unopenable file says why`() = runTest {
        val source = FakeSource().apply { export = AppResult.Failure(AppError.NoNetwork()) }
        val vm = AccountDataViewModel(FakeStore(), source, { ByteArrayOutputStream() }, kotlinx.coroutines.Dispatchers.Main)

        vm.exportWith { ByteArrayOutputStream() }
        advanceUntilIdle()
        assertThat(vm.export.value.failed).isTrue()
        assertThat(vm.export.value.message).startsWith("You're offline")

        val nowhere = AccountDataViewModel(FakeStore(), FakeSource(), { error("not used") }, kotlinx.coroutines.Dispatchers.Main)
        nowhere.exportWith { null }
        advanceUntilIdle()
        assertThat(nowhere.export.value.message).isEqualTo(AccountDataViewModel.EXPORT_NO_FILE)
    }

    @Test
    fun `verification needs a kind, sends it with the note, and says it is waiting`() = runTest {
        val source = FakeSource()
        val vm = VerificationViewModel(source)

        vm.submit()
        assertThat(source.asked).isEmpty()

        vm.select(VerificationKind.CREATOR)
        vm.onNoteChange("I make films")
        vm.submit()
        advanceUntilIdle()

        assertThat(source.asked).containsExactly("creator:I make films")
        assertThat(vm.state.value.submitted).isTrue()
        assertThat(vm.state.value.canSubmit).isFalse()
    }

    @Test
    fun `a pending request already on file is said plainly`() = runTest {
        val source = FakeSource().apply {
            verification = AppResult.Failure(
                AppError.Unknown(code = "BAD_REQUEST", statusCode = 400, message = "a pending verification request already exists for this user"),
            )
        }
        val vm = VerificationViewModel(source)
        vm.select(VerificationKind.BUSINESS)

        vm.submit()
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("You already have a request waiting for review.")
        assertThat(vm.state.value.submitted).isFalse()
    }

    @Test
    fun `the data rows act in place, and verification is a page`() {
        val sections = SettingsSections({}, {}, {}, {}, {}, {}, {}, {}, {})
        assertThat(settingsRowAction(SettingsRow.DATA_SAVER, sections, {}, {}, {})).isNull()
        assertThat(settingsRowAction(SettingsRow.DOWNLOAD_DATA, sections, {}, {}, {})).isNull()
        assertThat(VerificationKind.entries.map { it.wire })
            .containsExactly("creator", "business", "organization", "government").inOrder()
    }
}
