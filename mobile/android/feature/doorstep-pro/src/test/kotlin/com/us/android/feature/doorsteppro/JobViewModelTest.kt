package com.us.android.feature.doorsteppro

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.data.PhotosRequiredDto
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.domain.PhotoPhase
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.domain.VisitStep
import com.us.android.feature.doorsteppro.job.JobViewModel
import com.us.android.feature.doorsteppro.location.ProDuty
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Rule
import org.junit.Test
import java.time.Instant

/**
 * The job screen's guards end to end on a fake server: the photo gate keeps
 * the start code from being sent, the OTP lockout is shown from the server's
 * refusal and shuts entry, and a successful arrival is remembered for the
 * no-show wait.
 */
class JobViewModelTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val now = Instant.parse("2026-10-04T05:05:00Z")
    private val repository = FakeProRepository()
    private val memory = FakeVisitMemory()
    private val duty = ProDuty()

    private fun viewModel(): JobViewModel = JobViewModel(
        savedStateHandle = SavedStateHandle(mapOf("bookingId" to "b-1")),
        repository = repository,
        uploads = FakeUploads(),
        location = FakeLocation(),
        memory = memory,
        duty = duty,
        clock = ProClock { now },
    )

    private fun refused(status: Int, code: String, vararg details: Pair<String, Any>) = ProResult.Failure(
        ProError.Refused(
            status,
            code,
            "server text",
            JsonObject(details.associate { (k, v) -> k to if (v is Int) JsonPrimitive(v) else JsonPrimitive(v.toString()) }),
        ),
    )

    @Test
    fun `the start code is not sent until the before photos are taken`() {
        repository.jobResult = ProResult.Success(Jobs.job(status = "arrived", photos = PhotosRequiredDto(before = 2, after = 2, kitSeal = 0)))
        val vm = viewModel()
        assertThat(vm.state.value.actions?.step).isEqualTo(VisitStep.START_WITH_OTP)

        vm.onStartOtp("4821")
        vm.submitStart()
        assertThat(repository.otpsSent).isEmpty()
        assertThat(vm.state.value.message?.text).isEqualTo("Take 2 more before photos.")

        repeat(2) {
            vm.willCapture(PhotoPhase.BEFORE)
            vm.onPhoto(PickedPhoto(uri = "content://photo/$it", path = null))
        }
        assertThat(repository.photosSent).containsExactly("before", "before")
        assertThat(vm.state.value.missingToStart).isEmpty()

        repository.startResult = ProResult.Success(Jobs.job(status = "in_progress"))
        vm.submitStart()
        assertThat(repository.otpsSent).containsExactly("start:4821")
        assertThat(vm.state.value.actions?.step).isEqualTo(VisitStep.WORK)
        assertThat(vm.state.value.startOtp.code).isEmpty()
    }

    @Test
    fun `a wrong code shows the attempts left, and a lockout shuts entry until the server's time`() {
        repository.jobResult = ProResult.Success(Jobs.job(status = "arrived", photos = PhotosRequiredDto(0, 0, 0)))
        val vm = viewModel()

        repository.startResult = refused(422, "DOORSTEP_OTP_INVALID", "attempts_left" to 1)
        vm.onStartOtp("1111")
        vm.submitStart()
        assertThat(vm.state.value.startOtp.attemptsLeft).isEqualTo(1)
        assertThat(vm.state.value.startOtp.code).isEmpty()
        assertThat(vm.state.value.busy).isFalse()

        repository.startResult = refused(423, "DOORSTEP_OTP_LOCKED", "locked_until" to "2026-10-04T05:20:00Z")
        vm.onStartOtp("2222")
        vm.submitStart()
        assertThat(vm.state.value.startOtp.lockedUntil).isEqualTo(Instant.parse("2026-10-04T05:20:00Z"))

        // Locked: nothing more reaches the server, whatever is typed.
        vm.onStartOtp("3333")
        vm.submitStart()
        assertThat(repository.otpsSent).containsExactly("start:1111", "start:2222").inOrder()
    }

    @Test
    fun `a photos-required refusal adopts the server's count and says what is missing`() {
        repository.jobResult = ProResult.Success(Jobs.job(status = "in_progress", photos = PhotosRequiredDto(before = 0, after = 1, kitSeal = 0)))
        memory.finishedIds += "b-1"
        val vm = viewModel()
        assertThat(vm.state.value.actions?.step).isEqualTo(VisitStep.COMPLETE_WITH_OTP)

        vm.willCapture(PhotoPhase.AFTER)
        vm.onPhoto(PickedPhoto(uri = "content://photo/after", path = null))
        repository.completeResult = refused(422, "DOORSTEP_PHOTOS_REQUIRED", "phase" to "after", "required" to 1, "uploaded" to 0)
        vm.onEndOtp("5555")
        vm.submitComplete()
        assertThat(vm.state.value.photos[PhotoPhase.AFTER]).isEqualTo(0)
        assertThat(vm.state.value.message?.text).isEqualTo("Take 1 more after photo.")
    }

    @Test
    fun `arriving is remembered for the no-show wait and stops the travel cadence`() {
        repository.jobResult = ProResult.Success(Jobs.job(status = "en_route"))
        val vm = viewModel()
        assertThat(duty.travelling.value).isTrue()

        repository.arrivedResult = ProResult.Success(Jobs.job(status = "arrived"))
        vm.markArrived()
        assertThat(memory.arrived["b-1"]).isEqualTo(now)
        assertThat(duty.travelling.value).isFalse()
        assertThat(vm.state.value.actions?.noShowInSeconds).isEqualTo(15 * 60L)
    }

    @Test
    fun `a failed geo check says how far off the professional is`() {
        repository.jobResult = ProResult.Success(Jobs.job(status = "en_route"))
        val vm = viewModel()
        repository.arrivedResult = refused(422, "DOORSTEP_GEO_CHECK_FAILED", "distance_m" to 1450, "max_distance_m" to 300)
        vm.markArrived()
        assertThat(vm.state.value.message?.text).isEqualTo("You're 1.5 km from the address. Get within 300 m and try again.")
        assertThat(memory.arrived).isEmpty()
    }
}
