package com.us.android.feature.doorsteppro.data

import kotlinx.serialization.json.Json
import javax.inject.Inject

/**
 * The professional's doorstep-service seam. An interface so every ViewModel
 * tests on the JVM against a fake; [RealDoorstepProRepository] is the only
 * implementation that talks to the network.
 */
@Suppress("TooManyFunctions")
interface DoorstepProRepository {
    // Onboarding (A2)
    suspend fun apply(displayName: String, cityCode: String): ProResult<ProfessionalDto>
    suspend fun me(): ProResult<ProfessionalDto>
    suspend fun updateProfile(displayName: String?, photoMediaId: String?): ProResult<ProfessionalDto>
    suspend fun readiness(): ProResult<ProReadinessDto>
    suspend fun startDigiLocker(): ProResult<DigiLockerStartDto>
    suspend fun completeDigiLocker(code: String, state: String): ProResult<ProReadinessDto>
    suspend fun submitSelfie(mediaId: String): ProResult<KycCheckDto>
    suspend fun skillCatalogue(): ProResult<List<SkillDto>>
    suspend fun declareSkills(codes: List<String>): ProResult<List<ProSkillDto>>
    suspend fun uploadTradeCertificate(skillCode: String, mediaId: String, issuedOn: String, number: String?): ProResult<ProDocumentDto>
    suspend fun saveArea(zoneIds: List<String>, homeLat: Double, homeLng: Double, radiusM: Int): ProResult<ProAreaDto>
    suspend fun hours(): ProResult<WeeklyHoursDto>
    suspend fun saveHours(windows: List<HoursWindowDto>): ProResult<WeeklyHoursDto>
    suspend fun daysOff(): ProResult<List<DayOffDto>>
    suspend fun addDayOff(date: String, reason: String?): ProResult<DayOffDto>
    suspend fun removeDayOff(date: String): ProResult<Unit>
    suspend fun saveBank(holder: String, accountNumber: String, ifsc: String): ProResult<PayoutAccountDto>
    suspend fun uploadPoliceCertificate(mediaId: String, issuedOn: String, number: String?): ProResult<ProDocumentDto>
    suspend fun acceptAgreement(version: String): ProResult<ProReadinessDto>
    suspend fun savePan(pan: String): ProResult<ProReadinessDto>
    suspend fun serviceability(lat: Double, lng: Double): ProResult<ServiceabilityDto>

    // Duty and offers (A4)
    suspend fun dutyOn(location: LocationRequest): ProResult<DutyStateDto>
    suspend fun dutyOff(): ProResult<DutyStateDto>
    suspend fun postLocation(location: LocationRequest): ProResult<Unit>
    suspend fun offers(): ProResult<List<OfferDto>>
    suspend fun acceptOffer(offerId: String): ProResult<ProJobDto>
    suspend fun declineOffer(offerId: String, reason: String?): ProResult<Unit>
    suspend fun jobs(status: String?, cursor: String?): ProResult<ProJobPageDto>
    suspend fun job(bookingId: String): ProResult<ProJobDto>
    suspend fun realtimeToken(): ProResult<RealtimeTokenDto>

    // The visit (A5)
    suspend fun enRoute(bookingId: String): ProResult<ProJobDto>
    suspend fun arrived(bookingId: String, location: LocationRequest): ProResult<ProJobDto>
    suspend fun recordPhoto(bookingId: String, phase: String, mediaId: String, lat: Double?, lng: Double?): ProResult<PhotoDto>
    suspend fun start(bookingId: String, otp: String): ProResult<ProJobDto>
    suspend fun extraOptions(bookingId: String): ProResult<List<ExtraOptionDto>>
    suspend fun extras(bookingId: String): ProResult<List<ExtraDto>>
    suspend fun proposeExtra(bookingId: String, request: ExtraRequest): ProResult<ExtraDto>
    suspend fun withdrawExtra(bookingId: String, extraId: String): ProResult<Unit>
    suspend fun finish(bookingId: String): ProResult<ProJobDto>
    suspend fun complete(bookingId: String, otp: String): ProResult<ProJobDto>
    suspend fun customerNoShow(bookingId: String): ProResult<ProJobDto>
    suspend fun cancel(bookingId: String, reason: String): ProResult<Unit>
    suspend fun sos(bookingId: String, request: SosRequest): ProResult<IncidentDto>
    suspend fun unsafeExit(bookingId: String, request: SosRequest): ProResult<IncidentDto>
    suspend fun rateCustomer(bookingId: String, stars: Int, tags: List<String>, comment: String?): ProResult<RatingDto>
    suspend fun messages(bookingId: String, cursor: String?): ProResult<MessagePageDto>
    suspend fun sendMessage(bookingId: String, body: String): ProResult<MessageDto>
    suspend fun earnings(from: String?, to: String?): ProResult<EarningsDto>
}

@Suppress("TooManyFunctions")
class RealDoorstepProRepository @Inject constructor(
    private val api: DoorstepProApi,
    private val json: Json,
) : DoorstepProRepository {

    override suspend fun apply(displayName: String, cityCode: String) =
        proCall(json) { api.apply(ProApplyRequest(displayName = displayName, cityCode = cityCode)) }

    override suspend fun me() = proCall(json) { api.me() }

    override suspend fun updateProfile(displayName: String?, photoMediaId: String?) =
        proCall(json) { api.patchMe(ProPatchRequest(displayName = displayName, photoMediaId = photoMediaId)) }

    override suspend fun readiness() = proCall(json) { api.readiness() }

    override suspend fun startDigiLocker() = proCall(json) { api.digiLockerStart() }

    override suspend fun completeDigiLocker(code: String, state: String) =
        proCall(json) { api.digiLockerCallback(DigiLockerCallbackRequest(code = code, state = state)) }

    override suspend fun submitSelfie(mediaId: String) = proCall(json) { api.selfie(MediaRequest(mediaId)) }

    override suspend fun skillCatalogue() = proCall(json) { api.skills() }.map { it.items }

    override suspend fun declareSkills(codes: List<String>) =
        proCall(json) { api.putSkills(SkillsRequest(codes)) }.map { it.items }

    override suspend fun uploadTradeCertificate(skillCode: String, mediaId: String, issuedOn: String, number: String?) =
        proCall(json) {
            api.tradeCertificate(skillCode, CertificateRequest(mediaId = mediaId, issuedOn = issuedOn, certificateNumber = number))
        }

    override suspend fun saveArea(zoneIds: List<String>, homeLat: Double, homeLng: Double, radiusM: Int) =
        proCall(json) { api.putArea(ProAreaRequest(zoneIds = zoneIds, homeLat = homeLat, homeLng = homeLng, radiusM = radiusM)) }

    override suspend fun hours() = proCall(json) { api.hours() }

    override suspend fun saveHours(windows: List<HoursWindowDto>) = proCall(json) { api.putHours(WeeklyHoursDto(windows)) }

    override suspend fun daysOff() = proCall(json) { api.daysOff() }.map { it.items }

    override suspend fun addDayOff(date: String, reason: String?) =
        proCall(json) { api.addDayOff(DayOffDto(date = date, reason = reason?.takeIf { it.isNotBlank() })) }

    override suspend fun removeDayOff(date: String) = proUnitCall(json) { api.deleteDayOff(date) }

    override suspend fun saveBank(holder: String, accountNumber: String, ifsc: String) =
        proCall(json) { api.putBank(BankRequest(accountHolder = holder, accountNumber = accountNumber, ifsc = ifsc)) }

    override suspend fun uploadPoliceCertificate(mediaId: String, issuedOn: String, number: String?) =
        proCall(json) { api.policeCertificate(CertificateRequest(mediaId = mediaId, issuedOn = issuedOn, certificateNumber = number)) }

    override suspend fun acceptAgreement(version: String) = proCall(json) { api.acceptAgreement(AgreementRequest(version)) }

    override suspend fun savePan(pan: String) = proCall(json) { api.putPan(PanRequest(pan)) }

    override suspend fun serviceability(lat: Double, lng: Double) =
        proCall(json) { api.serviceability(ServiceabilityRequest(lat = lat, lng = lng)) }

    override suspend fun dutyOn(location: LocationRequest) = proCall(json) { api.dutyOn(location) }

    override suspend fun dutyOff() = proCall(json) { api.dutyOff() }

    override suspend fun postLocation(location: LocationRequest) = proUnitCall(json) { api.location(location) }

    override suspend fun offers() = proCall(json) { api.offers() }.map { it.items }

    override suspend fun acceptOffer(offerId: String) = proCall(json) { api.acceptOffer(offerId) }

    override suspend fun declineOffer(offerId: String, reason: String?) =
        proUnitCall(json) { api.declineOffer(offerId, DeclineRequest(reason)) }

    override suspend fun jobs(status: String?, cursor: String?) = proCall(json) { api.jobs(status, cursor) }

    override suspend fun job(bookingId: String) = proCall(json) { api.job(bookingId) }

    override suspend fun realtimeToken() = proCall(json) { api.realtimeToken() }

    override suspend fun enRoute(bookingId: String) = proCall(json) { api.enRoute(bookingId) }

    override suspend fun arrived(bookingId: String, location: LocationRequest) = proCall(json) { api.arrived(bookingId, location) }

    override suspend fun recordPhoto(bookingId: String, phase: String, mediaId: String, lat: Double?, lng: Double?) =
        proCall(json) { api.photo(bookingId, PhotoRequest(phase = phase, mediaId = mediaId, lat = lat, lng = lng)) }

    override suspend fun start(bookingId: String, otp: String) = proCall(json) { api.start(bookingId, OtpRequest(otp)) }

    override suspend fun extraOptions(bookingId: String) = proCall(json) { api.extraOptions(bookingId) }.map { it.items }

    override suspend fun extras(bookingId: String) = proCall(json) { api.extras(bookingId) }.map { it.items }

    override suspend fun proposeExtra(bookingId: String, request: ExtraRequest) = proCall(json) { api.proposeExtra(bookingId, request) }

    override suspend fun withdrawExtra(bookingId: String, extraId: String) = proUnitCall(json) { api.withdrawExtra(bookingId, extraId) }

    override suspend fun finish(bookingId: String) = proCall(json) { api.finish(bookingId) }

    override suspend fun complete(bookingId: String, otp: String) = proCall(json) { api.complete(bookingId, OtpRequest(otp)) }

    override suspend fun customerNoShow(bookingId: String) = proCall(json) { api.customerNoShow(bookingId) }

    override suspend fun cancel(bookingId: String, reason: String) = proUnitCall(json) { api.cancel(bookingId, CancelRequest(reason)) }

    override suspend fun sos(bookingId: String, request: SosRequest) = proCall(json) { api.sos(bookingId, request) }

    override suspend fun unsafeExit(bookingId: String, request: SosRequest) = proCall(json) { api.unsafeExit(bookingId, request) }

    override suspend fun rateCustomer(bookingId: String, stars: Int, tags: List<String>, comment: String?) =
        proCall(json) {
            api.rateCustomer(
                bookingId,
                RatingRequest(stars = stars, tags = tags.takeIf { it.isNotEmpty() }, comment = comment?.takeIf { it.isNotBlank() }),
            )
        }

    override suspend fun messages(bookingId: String, cursor: String?) = proCall(json) { api.messages(bookingId, cursor) }

    override suspend fun sendMessage(bookingId: String, body: String) = proCall(json) { api.sendMessage(bookingId, MessageRequest(body)) }

    override suspend fun earnings(from: String?, to: String?) = proCall(json) { api.earnings(from, to) }
}
