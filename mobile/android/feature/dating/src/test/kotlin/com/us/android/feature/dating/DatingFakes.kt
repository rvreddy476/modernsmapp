package com.us.android.feature.dating

import android.app.Activity
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.location.Coordinates
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.network.AllowanceDto
import com.us.android.feature.dating.network.AllowancesDto
import com.us.android.feature.dating.network.RewindDto
import com.us.android.feature.dating.network.BlockRequest
import com.us.android.feature.dating.network.BlockedDto
import com.us.android.feature.dating.network.BlockedPersonDto
import com.us.android.feature.dating.network.BlocksDto
import com.us.android.feature.dating.network.BotheredDto
import com.us.android.feature.dating.network.BotheredRequest
import com.us.android.feature.dating.network.ClientConfigDto
import com.us.android.feature.dating.network.CommentFilterDto
import com.us.android.feature.dating.network.CommentFilterRequest
import com.us.android.feature.dating.network.HideKnownDto
import com.us.android.feature.dating.network.HideKnownRequest
import com.us.android.feature.dating.network.KindCheckDto
import com.us.android.feature.dating.network.KindCheckRequest
import com.us.android.feature.dating.network.ClosedDto
import com.us.android.feature.dating.network.ConsentRequest
import com.us.android.feature.dating.network.ConsentStateDto
import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.AttachPhotoRequest
import com.us.android.feature.dating.network.DataExportDto
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.network.DateCheckinDto
import com.us.android.feature.dating.network.DateFeedbackDto
import com.us.android.feature.dating.network.DateFeedbackRequest
import com.us.android.feature.dating.network.PastMatchesDto
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.DatingPhotoDto
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.DeleteProfileRequest
import com.us.android.feature.dating.network.ExplainDto
import com.us.android.feature.dating.network.ExtendDto
import com.us.android.feature.dating.network.FirstMoveRequest
import com.us.android.feature.dating.network.FirstMoveSettingsDto
import com.us.android.feature.dating.network.OpeningAnswerDto
import com.us.android.feature.dating.network.OpeningAnswerRequest
import com.us.android.feature.dating.network.OpeningQuestionDto
import com.us.android.feature.dating.network.LikedYouDto
import com.us.android.feature.dating.network.LikedYouItemDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.MyLocationShareDto
import com.us.android.feature.dating.network.MyLocationSharesDto
import com.us.android.feature.dating.network.PanicDto
import com.us.android.feature.dating.network.PanicRequest
import com.us.android.feature.dating.network.PassDto
import com.us.android.feature.dating.network.PassRequest
import com.us.android.feature.dating.network.PauseRequest
import com.us.android.feature.dating.network.PicksDto
import com.us.android.feature.dating.network.TravelDto
import com.us.android.feature.dating.network.TravelRequest
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PreferencesRequest
import com.us.android.feature.dating.network.PremiumCatalogueDto
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.PremiumPaymentDto
import com.us.android.feature.dating.network.PremiumPurchaseRequest
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import com.us.android.feature.dating.network.PrivacyDto
import com.us.android.feature.dating.network.PrivacyUpdateRequest
import com.us.android.feature.dating.network.PromptAnswerDto
import com.us.android.feature.dating.network.PromptAnswerRequest
import com.us.android.feature.dating.network.PromptCatalogItemDto
import com.us.android.feature.dating.network.PromptClipRequest
import com.us.android.feature.dating.network.PromptClipViewDto
import com.us.android.feature.dating.network.ProfileOptionsDto
import com.us.android.feature.dating.network.CardPhotoDto
import com.us.android.feature.dating.network.DetailPromptDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.network.PulseCardDto
import com.us.android.feature.dating.network.PulseProfileDto
import com.us.android.feature.dating.network.PulseTodayDto
import com.us.android.feature.dating.network.ReadReceiptsDto
import com.us.android.feature.dating.network.ReadReceiptsRequest
import com.us.android.feature.dating.network.RemovedDto
import com.us.android.feature.dating.network.ReportRequest
import com.us.android.feature.dating.network.ReportResultDto
import com.us.android.feature.dating.network.SelfieChallengeDto
import com.us.android.feature.dating.network.SelfieResultDto
import com.us.android.feature.dating.network.SelfieStatusDto
import com.us.android.feature.dating.network.SelfieSubmitRequest
import com.us.android.feature.dating.network.ShareLocationDto
import com.us.android.feature.dating.network.ShareLocationRequest
import com.us.android.feature.dating.network.SharedLocationDto
import com.us.android.feature.dating.network.SharedWithMeDto
import com.us.android.feature.dating.network.SharedWithMeItemDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.network.SparkDeclineDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.network.SparkRequest
import com.us.android.feature.dating.network.StashDto
import com.us.android.feature.dating.network.StashRequest
import com.us.android.feature.dating.network.StatusDto
import com.us.android.feature.dating.network.StopShareDto
import com.us.android.feature.dating.network.TrustedContactDto
import com.us.android.feature.dating.network.TrustedContactRequest
import com.us.android.feature.dating.network.TrustedContactsDto
import com.us.android.feature.dating.network.UpdatePhotoRequest
import com.us.android.feature.dating.network.UpsertProfileRequest
import com.us.android.feature.dating.network.UnblockedDto
import com.us.android.feature.dating.network.VerificationStatusDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import kotlinx.serialization.KSerializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.Response
import java.io.File
import java.io.IOException

val testJson = NetworkModule.provideJson()

const val ME = "me-1"

private val JSON_TYPE = "application/json".toMediaType()

fun <T> ok(value: T): Response<ApiEnvelope<T>> = Response.success(ApiEnvelope(data = value))

/** A dating-service refusal: the envelope WITH meta, as every dating handler writes it. */
fun <T> refused(status: Int, code: String, details: String? = null): Response<ApiEnvelope<T>> {
    val detailsPart = details?.let { ",\"details\":$it" }.orEmpty()
    return Response.error(status, """{"error":{"code":"$code","message":"refused"$detailsPart},"meta":{}}""".toResponseBody(JSON_TYPE))
}

/** The gateway's pilot-allowlist 404: no meta. */
fun <T> gatewayNotFound(): Response<ApiEnvelope<T>> =
    Response.error(404, """{"error":{"code":"NOT_FOUND","message":"Not found"}}""".toResponseBody(JSON_TYPE))

fun fixtureText(name: String): String = File("src/test/resources/contracts/$name").readText()

/** A golden fixture decoded as production does. */
fun <T> fixture(name: String, serializer: KSerializer<T>): T =
    checkNotNull(testJson.decodeFromString(ApiEnvelope.serializer(serializer), fixtureText(name)).data)

/** A golden error fixture served with its HTTP [status], exactly as the server wrote it. */
fun <T> refusedWithFixture(status: Int, name: String): Response<ApiEnvelope<T>> =
    Response.error(status, fixtureText(name).toResponseBody(JSON_TYPE))

/** A golden error fixture for a route whose success body is NOT the envelope (`/pulse/today`, `/picks`). */
fun <T> rawRefusedWithFixture(status: Int, name: String): Response<T> =
    Response.error(status, fixtureText(name).toResponseBody(JSON_TYPE))

fun <T> offline(): Response<ApiEnvelope<T>> = throw IOException("offline")

fun profile(
    status: String = "active",
    gender: String? = "woman",
    intent: String = "casual",
    city: String? = "Hyderabad",
    firstName: String? = "Asha",
    birthDate: String? = "1995-04-12T00:00:00Z",
) = DatingProfileDto(
    userId = ME,
    intent = intent,
    gender = gender,
    city = city,
    firstName = firstName,
    birthDate = birthDate,
    trustTier = "selfie",
    profileStatus = status,
)

fun card(
    userId: String,
    bucket: String? = "lt_5_km",
    label: String? = "< 5 km",
    detail: ProfileDetailDto? = null,
    city: String = "Hyderabad",
    travelling: Boolean = false,
    trustTier: String = "selfie",
) = PulseCardDto(
    candidateId = userId,
    profile = PulseProfileDto(
        userId = userId,
        firstName = "Person $userId",
        age = 30,
        city = city,
        distanceBucket = bucket,
        distanceLabel = label,
        primaryPhotoUrl = "/v1/dating/photos/photo-$userId/full",
        trustTier = trustTier,
        travelling = travelling,
        detail = detail,
    ),
)

/** One gallery photo whose url matches its own [state], the way the server sends it. */
fun galleryPhoto(id: String, state: String) =
    CardPhotoDto(id = id, url = "/v1/dating/photos/$id/$state", state = state)

/** The pre-match detail block: the bio, prompt answers, languages and gallery. */
fun detail(
    bio: String = "Filter coffee and long drives.",
    prompts: List<DetailPromptDto> = listOf(
        DetailPromptDto(promptId = 1, question = "My ideal Sunday is...", answer = "Dosa and a bookshop."),
        DetailPromptDto(promptId = 2, question = "I get nerdy about...", answer = "Carnatic ragas."),
    ),
    languages: List<String> = listOf("telugu", "english"),
    photos: List<CardPhotoDto> = listOf(galleryPhoto("p-1", "full")),
) = ProfileDetailDto(bio = bio, prompts = prompts, languages = languages, photos = photos)

/** The compact card the server now sends inline on matches and incoming sparks. */
fun person(
    userId: String,
    name: String = "Person $userId",
    age: Int = 30,
    photoState: String = "full",
    bucket: String? = "lt_5_km",
    verified: Boolean = true,
    city: String = "Hyderabad",
    intent: String = "casual",
    // Null by BOTH defaults is the hidden case: a person who hides last active,
    // which is what a new profile does, and the server then omits both fields.
    lastActiveBucket: String? = null,
    lastActiveLabel: String? = null,
    detail: ProfileDetailDto? = null,
    travelling: Boolean = false,
) = DatingPersonDto(
    userId = userId,
    firstName = name,
    age = age,
    primaryPhotoId = "photo-$userId",
    primaryPhotoUrl = "/v1/dating/photos/photo-$userId/$photoState",
    photoState = photoState,
    verified = verified,
    trustTier = if (verified) "selfie" else "phone",
    distanceBucket = bucket,
    distanceLabel = bucket?.let { "server label" },
    city = city,
    intent = intent,
    lastActiveBucket = lastActiveBucket,
    lastActiveLabel = lastActiveLabel,
    travelling = travelling,
    detail = detail,
)

fun match(id: String, other: String, card: DatingPersonDto? = person(other)) =
    MatchDto(id = id, userA = ME, userB = other, status = "matched", conversationId = "conv-$id", person = card)

fun spark(id: String, from: String, card: DatingPersonDto? = person(from)) =
    SparkDto(id = id, fromUserId = from, toUserId = ME, targetKind = "photo", targetRef = "0", person = card)

/** An unlocked "liked you" card: the sender's person card, their note and their photo route. */
fun likedYouOpen(sparkId: String, from: String, superSpark: Boolean = false, card: DatingPersonDto? = person(from), note: String? = "Hi") =
    LikedYouItemDto(sparkId = sparkId, superSpark = superSpark, createdAt = "t", photoUrl = card?.primaryPhotoUrl.orEmpty(), person = card, note = note)

/** A locked "liked you" card, exactly as the server sends one: the spark, the flag and the blurred route. */
fun likedYouLocked(sparkId: String, superSpark: Boolean = false) =
    LikedYouItemDto(sparkId = sparkId, superSpark = superSpark, createdAt = "t", photoUrl = "/v1/dating/liked-you/$sparkId/photo")

/**
 * A trusted contact as `GET /safety/trusted-contacts` lists it. [card] is null
 * for a contact whose profile was deleted or purged.
 */
fun trustedContact(contactId: String, card: DatingPersonDto? = person(contactId)) =
    TrustedContactDto(contactId = contactId, shareLocationOnPanic = true, person = card)

/** The photo URL resolver, against a fixed base so a test can assert the whole URL. */
fun photoUrls() = DatingPhotoUrls(
    ApiConfig(baseUrl = "https://api.test", wsBaseUrl = "", clientVersion = "t", environment = "test", isDebug = true),
)

fun consents(vararg granted: ConsentType) = ConsentsDto(
    currentPolicyVersion = "v1.0-2026-04-29",
    consents = ConsentType.entries.map { ConsentStateDto(consentType = it.wire, granted = it in granted) },
)

/** A dating-service that answers from fields the test sets, and records what it was asked, in order. */
@Suppress("TooManyFunctions")
class FakeDatingApi : DatingApi {

    /** Every call, in order, by a short name ("consents", "upsert", "consent:sensitive_religion=true", …). */
    val calls = mutableListOf<String>()

    val granted = mutableSetOf<ConsentType>()
    var consentsResponse: (() -> Response<ApiEnvelope<ConsentsDto>>)? = null
    var profileResponse: () -> Response<ApiEnvelope<DatingProfileDto>> = { ok(com.us.android.feature.dating.profile()) }
    var preferences = PreferencesDto(userId = ME, interestedInGender = "man", distanceKm = 25)
    val preferenceWrites = mutableListOf<PreferencesRequest>()
    var preferencesWriteResponse: ((PreferencesRequest) -> Response<ApiEnvelope<PreferencesDto>>)? = null
    val upserts = mutableListOf<UpsertProfileRequest>()
    var upsertResponse: (UpsertProfileRequest) -> Response<ApiEnvelope<DatingProfileDto>> = { ok(com.us.android.feature.dating.profile("draft")) }

    var pulse: List<PulseCardDto> = emptyList()

    /** Overrides the whole `/pulse/today` body, for the envelope's two shapes. */
    var pulseResponse: (() -> Response<PulseTodayDto>)? = null

    var incoming: List<SparkDto> = emptyList()
    var matches: List<MatchDto> = emptyList()
    val sparks = mutableListOf<SparkRequest>()
    var sparkResponse: (SparkRequest) -> Response<ApiEnvelope<SparkCreatedDto>> = { ok(SparkCreatedDto()) }

    /** Holds a spark open until the test lets it go, so the action can be seen in flight. */
    var sparkGate: (suspend () -> Unit)? = null
    val passes = mutableListOf<String>()
    var passResponse: ((String) -> Response<ApiEnvelope<PassDto>>)? = null
    val declines = mutableListOf<String>()
    val accepts = mutableListOf<String>()
    var acceptResponse: (String) -> Response<ApiEnvelope<SparkCreatedDto>> = { ok(SparkCreatedDto()) }

    /** The compact cards `GET /people/:userId` can serve, by user id. */
    var people: Map<String, DatingPersonDto> = emptyMap()

    val blocks = mutableListOf<String>()
    val unblocks = mutableListOf<String>()
    var blocked: List<BlockedPersonDto> = emptyList()

    var myShares: List<MyLocationShareDto> = emptyList()
    var sharedWithMe: List<SharedWithMeItemDto> = emptyList()
    val stops = mutableListOf<String>()
    var stopShareResponse: ((String) -> Response<ApiEnvelope<StopShareDto>>)? = null

    var verificationResponse: () -> Response<ApiEnvelope<VerificationStatusDto>> =
        { ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "none", attemptsLeftToday = 5, attemptsPerDay = 5, windowHours = 24), nextStep = "submit_selfie")) }

    var blockResponse: () -> Response<ApiEnvelope<BlockedDto>> = { ok(BlockedDto(blocked = true)) }
    val reports = mutableListOf<ReportRequest>()
    var reportResponse: (ReportRequest) -> Response<ApiEnvelope<ReportResultDto>> =
        { ok(ReportResultDto(id = "report-1", targetId = it.targetId, reason = it.reason, blocked = true, autoBlocked = true)) }
    var trusted: List<TrustedContactDto> = emptyList()

    var challengeResponse: () -> Response<ApiEnvelope<SelfieChallengeDto>> =
        { ok(SelfieChallengeDto(challengeId = "challenge-${calls.count { it == "challenge" }}", instruction = "blink_twice", maxDurationMs = 4000)) }
    val selfieSubmissions = mutableListOf<SelfieSubmitRequest>()
    var selfieResponse: (SelfieSubmitRequest) -> Response<ApiEnvelope<SelfieResultDto>> =
        { ok(SelfieResultDto(status = "passed", passed = true, trustTier = "selfie", profileStatus = "active", attemptsRemaining = 4)) }

    val purchaseRequests = mutableListOf<PremiumPurchaseRequest>()
    var purchaseResponse: (PremiumPurchaseRequest) -> Response<ApiEnvelope<PremiumPurchaseResultDto>> =
        { fixtureResult(it) }

    /** Each payment-status read takes the next answer; the last one repeats. */
    val paymentAnswers = ArrayDeque<() -> Response<ApiEnvelope<PremiumPaymentDto>>>()
    var paymentReads = 0
        private set

    private fun fixtureResult(request: PremiumPurchaseRequest): Response<ApiEnvelope<PremiumPurchaseResultDto>> {
        val base = fixture("premium_purchase_post_201.json", PremiumPurchaseResultDto.serializer())
        return ok(base.copy(purchase = base.purchase.copy(id = "purchase-${purchaseRequests.size}", product = request.product, idempotencyKey = request.idempotencyKey)))
    }

    override suspend fun consents(): Response<ApiEnvelope<ConsentsDto>> {
        calls += "consents"
        return consentsResponse?.invoke() ?: ok(consents(*granted.toTypedArray()))
    }

    override suspend fun setConsent(type: String, body: ConsentRequest): Response<ApiEnvelope<ConsentsDto>> {
        calls += "consent:$type=${body.granted}"
        val consent = checkNotNull(ConsentType.fromWire(type))
        if (body.granted) granted += consent else granted -= consent
        return ok(consents(*granted.toTypedArray()))
    }

    override suspend fun profile(): Response<ApiEnvelope<DatingProfileDto>> {
        calls += "profile"
        return profileResponse()
    }

    override suspend fun upsertProfile(body: UpsertProfileRequest): Response<ApiEnvelope<DatingProfileDto>> {
        calls += "upsert"
        upserts += body
        return upsertResponse(body)
    }

    override suspend fun pause(body: PauseRequest) = ok(com.us.android.feature.dating.profile(if (body.paused) "paused" else "active"))

    override suspend fun deleteProfile(body: DeleteProfileRequest) = ok(StatusDto("deleted"))

    /** What `GET /profile/privacy` answers. */
    var privacyState = PrivacyDto()
    val privacyWrites = mutableListOf<PrivacyUpdateRequest>()
    var privacyWriteResponse: ((PrivacyUpdateRequest) -> Response<ApiEnvelope<PrivacyDto>>)? = null

    override suspend fun privacy() = ok(privacyState)

    override suspend fun updatePrivacy(body: PrivacyUpdateRequest): Response<ApiEnvelope<PrivacyDto>> {
        calls += "privacy:write"
        privacyWrites += body
        return privacyWriteResponse?.invoke(body) ?: ok(PrivacyDto(incognito = body.incognito ?: false))
    }

    /** `GET /profile/options` (mechanic M6). The default is the server's golden. */
    var profileOptionsResponse: () -> Response<ApiEnvelope<ProfileOptionsDto>> =
        { ok(fixture("profile_options_get_200.json", ProfileOptionsDto.serializer())) }

    override suspend fun profileOptions(): Response<ApiEnvelope<ProfileOptionsDto>> {
        calls += "profile-options"
        return profileOptionsResponse()
    }

    override suspend fun preferences(): Response<ApiEnvelope<PreferencesDto>> {
        calls += "preferences"
        return ok(preferences)
    }

    override suspend fun updatePreferences(body: PreferencesRequest): Response<ApiEnvelope<PreferencesDto>> {
        calls += "preferences:write"
        preferenceWrites += body
        return preferencesWriteResponse?.invoke(body)
            ?: ok(preferences.copy(interestedInGender = body.interestedInGender, dealbreakers = body.dealbreakers ?: preferences.dealbreakers))
    }

    override suspend fun myPhotos(): Response<ApiEnvelope<List<DatingPhotoDto>>> = ok(emptyList())

    override suspend fun attachPhoto(body: AttachPhotoRequest) = ok(DatingPhotoDto(id = "photo-1", mediaId = body.mediaId, moderationStatus = "approved"))

    override suspend fun updatePhoto(id: String, body: UpdatePhotoRequest) = ok(DatingPhotoDto(id = id))

    override suspend fun deletePhoto(id: String) = ok(StatusDto("deleted"))

    override suspend fun promptCatalog() = ok(listOf(PromptCatalogItemDto(1, "My ideal Sunday is...")))

    override suspend fun prompts(): Response<ApiEnvelope<List<PromptAnswerDto>>> = promptsResponse()

    override suspend fun answerPrompt(promptId: Int, body: PromptAnswerRequest) = ok(PromptAnswerDto(promptId = promptId, answer = body.answer))

    override suspend fun deletePrompt(promptId: Int) = ok(StatusDto("deleted"))

    // ── Mechanic M15: prompt clips ──────────────────────────────────────────

    /** `GET /prompts`; null data by default, as before. */
    var promptsResponse: () -> Response<ApiEnvelope<List<PromptAnswerDto>>> = { Response.success(ApiEnvelope(data = null)) }
    val clipWrites = mutableListOf<Pair<Int, String>>()

    /** `PUT /prompts/:promptId/clip`. The default is the approved golden, for the prompt asked about. */
    var clipResponse: (promptId: Int, mediaId: String) -> Response<ApiEnvelope<PromptClipViewDto>> =
        { promptId, _ -> ok(fixture("prompt_clip_put_200_approved.json", PromptClipViewDto.serializer()).copy(promptId = promptId)) }
    val clipDeletes = mutableListOf<Int>()
    var clipDeleteResponse: (promptId: Int) -> Response<ApiEnvelope<StatusDto>> = { ok(StatusDto("deleted")) }

    override suspend fun putPromptClip(promptId: Int, body: PromptClipRequest): Response<ApiEnvelope<PromptClipViewDto>> {
        calls += "clip:put"
        clipWrites += promptId to body.mediaId
        return clipResponse(promptId, body.mediaId)
    }

    override suspend fun deletePromptClip(promptId: Int): Response<ApiEnvelope<StatusDto>> {
        calls += "clip:delete"
        clipDeletes += promptId
        return clipDeleteResponse(promptId)
    }

    override suspend fun selfieChallenge(): Response<ApiEnvelope<SelfieChallengeDto>> {
        calls += "challenge"
        return challengeResponse()
    }

    override suspend fun submitSelfie(body: SelfieSubmitRequest): Response<ApiEnvelope<SelfieResultDto>> {
        calls += "selfie"
        selfieSubmissions += body
        return selfieResponse(body)
    }

    override suspend fun pulseToday(): Response<PulseTodayDto> {
        calls += "pulse"
        return pulseResponse?.invoke() ?: Response.success(PulseTodayDto(data = pulse))
    }

    override suspend fun explain(targetUserId: String) = ok(ExplainDto())

    /** `GET /allowances`. The default is every mechanic OFF: only sparks, as the server sends with no flags set. */
    var allowancesResponse: () -> Response<ApiEnvelope<AllowancesDto>> =
        { ok(AllowancesDto(sparks = AllowanceDto(dailyLimit = 50, remainingToday = 50))) }

    var rewindResponse: () -> Response<ApiEnvelope<RewindDto>> = { refused(409, "REWIND_NOTHING_TO_UNDO") }

    override suspend fun allowances(): Response<ApiEnvelope<AllowancesDto>> {
        calls += "allowances"
        return allowancesResponse()
    }

    override suspend fun rewind(): Response<ApiEnvelope<RewindDto>> {
        calls += "rewind"
        return rewindResponse()
    }

    /** Every pass body, in order, beside [passes]' ids: the source (mechanic M7) lives here. */
    val passBodies = mutableListOf<PassRequest>()

    override suspend fun pass(candidateId: String, body: PassRequest): Response<ApiEnvelope<PassDto>> {
        passes += candidateId
        passBodies += body
        return passResponse?.invoke(candidateId) ?: ok(PassDto(passed = true, candidateId = candidateId))
    }

    // ── Mechanic M7: daily picks ────────────────────────────────────────────

    /** Every `GET /picks` zone, in order; null is a read without `tz`. */
    val picksReads = mutableListOf<String?>()

    /** `GET /picks` by zone. The default is the server's flag OFF, as the golden writes it. */
    var picksResponse: (tz: String?) -> Response<PicksDto> = { rawRefusedWithFixture(404, "picks_get_404_not_enabled.json") }

    override suspend fun picks(tz: String?): Response<PicksDto> {
        calls += "picks"
        picksReads += tz
        return picksResponse(tz)
    }

    // ── Mechanic M8: travel ─────────────────────────────────────────────────

    /** `GET /travel`. The default is the server's flag OFF, as the golden writes it. */
    var travelResponse: () -> Response<ApiEnvelope<TravelDto>> = { refusedWithFixture(404, "travel_get_404_not_enabled.json") }
    val travelWrites = mutableListOf<TravelRequest>()
    var travelWriteResponse: (TravelRequest) -> Response<ApiEnvelope<TravelDto>> =
        { ok(fixture("travel_put_200.json", TravelDto.serializer())) }
    var travelEndResponse: () -> Response<ApiEnvelope<TravelDto>> =
        { ok(fixture("travel_get_200.json", TravelDto.serializer()).copy(available = true)) }

    override suspend fun travel(): Response<ApiEnvelope<TravelDto>> {
        calls += "travel"
        return travelResponse()
    }

    override suspend fun startTravel(body: TravelRequest): Response<ApiEnvelope<TravelDto>> {
        calls += "travel:start"
        travelWrites += body
        return travelWriteResponse(body)
    }

    override suspend fun endTravel(): Response<ApiEnvelope<TravelDto>> {
        calls += "travel:end"
        return travelEndResponse()
    }

    override suspend fun spark(body: SparkRequest): Response<ApiEnvelope<SparkCreatedDto>> {
        calls += "spark"
        sparks += body
        sparkGate?.invoke()
        return sparkResponse(body)
    }

    override suspend fun incomingSparks(limit: Int): Response<ApiEnvelope<List<SparkDto>>> {
        calls += "incoming"
        return ok(incoming)
    }

    /** `GET /liked-you`, by (limit, offset). The default is an empty, unlocked grid: the gate off. */
    var likedYouResponse: (limit: Int, offset: Int) -> Response<ApiEnvelope<LikedYouDto>> =
        { _, _ -> ok(LikedYouDto(unlocked = true)) }
    val likedYouReads = mutableListOf<Pair<Int, Int>>()

    override suspend fun likedYou(limit: Int, offset: Int): Response<ApiEnvelope<LikedYouDto>> {
        calls += "liked-you"
        likedYouReads += limit to offset
        return likedYouResponse(limit, offset)
    }

    var declineResponse: (String) -> Response<ApiEnvelope<SparkDeclineDto>> = { ok(SparkDeclineDto(declined = true, sparkId = it)) }

    override suspend fun declineSpark(id: String): Response<ApiEnvelope<SparkDeclineDto>> {
        calls += "decline"
        declines += id
        return declineResponse(id)
    }

    override suspend fun acceptSpark(id: String): Response<ApiEnvelope<SparkCreatedDto>> {
        calls += "accept"
        accepts += id
        return acceptResponse(id)
    }

    override suspend fun person(userId: String): Response<ApiEnvelope<DatingPersonDto>> {
        calls += "person"
        return people[userId]?.let { ok(it) } ?: refused(404, "NOT_FOUND")
    }

    override suspend fun verificationStatus(): Response<ApiEnvelope<VerificationStatusDto>> {
        calls += "verification"
        return verificationResponse()
    }

    override suspend fun blocks(): Response<ApiEnvelope<BlocksDto>> {
        calls += "blocks"
        return ok(BlocksDto(items = blocked))
    }

    override suspend fun unblock(userId: String): Response<ApiEnvelope<UnblockedDto>> {
        unblocks += userId
        val had = blocked.any { it.userId == userId }
        blocked = blocked.filterNot { it.userId == userId }
        return ok(UnblockedDto(unblocked = true, removed = had))
    }

    override suspend fun stash(body: StashRequest) = ok(StashDto(userId = ME, candidateId = body.candidateId))

    override suspend fun stashed() = ok(emptyList<StashDto>())

    override suspend fun unstash(candidateId: String) = ok(RemovedDto(removed = true))

    /** Overrides `GET /matches`, for its refusals. */
    var matchesResponse: (() -> Response<ApiEnvelope<List<MatchDto>>>)? = null

    override suspend fun matches(status: String?): Response<ApiEnvelope<List<MatchDto>>> {
        calls += "matches"
        return matchesResponse?.invoke() ?: ok(matches)
    }

    override suspend fun match(id: String): Response<ApiEnvelope<MatchDto>> =
        matches.firstOrNull { it.id == id }?.let { ok(it) } ?: refused(404, "NOT_FOUND")

    override suspend fun closeMatch(id: String) = ok(ClosedDto(closed = true))

    // ── Mechanic M5: first move ─────────────────────────────────────────────

    /** `GET /first-move`. The default is the server's flag OFF, as the golden writes it. */
    var firstMoveResponse: () -> Response<ApiEnvelope<FirstMoveSettingsDto>> =
        { refusedWithFixture(404, "first_move_get_404_not_enabled.json") }
    val firstMoveWrites = mutableListOf<FirstMoveRequest>()

    /** What the server holds for `PUT /first-move`'s default answer. */
    var firstMoveStored = FirstMoveSettingsDto(maxQuestions = 3, maxLength = 140)

    /** `PUT /first-move`. The default stores what was asked as the server does: an absent field is unchanged. */
    var firstMoveWriteResponse: (FirstMoveRequest) -> Response<ApiEnvelope<FirstMoveSettingsDto>> = { body ->
        firstMoveStored = firstMoveStored.copy(
            enabled = body.enabled ?: firstMoveStored.enabled,
            questions = body.questions?.mapIndexed { i, text -> OpeningQuestionDto(id = "q-$i", text = text) } ?: firstMoveStored.questions,
        )
        ok(firstMoveStored)
    }

    override suspend fun firstMove(): Response<ApiEnvelope<FirstMoveSettingsDto>> {
        calls += "first-move"
        return firstMoveResponse()
    }

    override suspend fun updateFirstMove(body: FirstMoveRequest): Response<ApiEnvelope<FirstMoveSettingsDto>> {
        calls += "first-move:write"
        firstMoveWrites += body
        return firstMoveWriteResponse(body)
    }

    val openingAnswers = mutableListOf<Pair<String, OpeningAnswerRequest>>()
    var openingAnswerResponse: (matchId: String, OpeningAnswerRequest) -> Response<ApiEnvelope<OpeningAnswerDto>> =
        { matchId, _ -> ok(OpeningAnswerDto(sent = true, conversationId = "conv-$matchId")) }

    override suspend fun openingAnswer(id: String, body: OpeningAnswerRequest): Response<ApiEnvelope<OpeningAnswerDto>> {
        calls += "opening-answer"
        openingAnswers += id to body
        return openingAnswerResponse(id, body)
    }

    val extends = mutableListOf<String>()
    var extendResponse: (matchId: String) -> Response<ApiEnvelope<ExtendDto>> =
        { ok(fixture("match_extend_post_200_free.json", ExtendDto.serializer())) }

    override suspend fun extendMatch(id: String): Response<ApiEnvelope<ExtendDto>> {
        calls += "extend"
        extends += id
        return extendResponse(id)
    }

    // ── Mechanic M9: read receipts ──────────────────────────────────────────

    /** `GET /read-receipts`. The default is the server's flag OFF, as the golden writes it. */
    var readReceiptsResponse: () -> Response<ApiEnvelope<ReadReceiptsDto>> =
        { refusedWithFixture(404, "read_receipts_get_404_not_enabled.json") }
    val readReceiptsWrites = mutableListOf<ReadReceiptsRequest>()

    /** `PUT /read-receipts`. The default answers as a pass holder's server does. */
    var readReceiptsWriteResponse: (ReadReceiptsRequest) -> Response<ApiEnvelope<ReadReceiptsDto>> =
        { ok(ReadReceiptsDto(enabled = it.enabled, active = it.enabled, available = true)) }

    override suspend fun readReceipts(): Response<ApiEnvelope<ReadReceiptsDto>> {
        calls += "read-receipts"
        return readReceiptsResponse()
    }

    override suspend fun updateReadReceipts(body: ReadReceiptsRequest): Response<ApiEnvelope<ReadReceiptsDto>> {
        calls += "read-receipts:write"
        readReceiptsWrites += body
        return readReceiptsWriteResponse(body)
    }

    // ── Mechanic M14: after-date check-ins ──────────────────────────────────

    /** `GET /date-checkins`. The default is the server's flag OFF, as the golden for the POST writes it. */
    var dateCheckinsResponse: () -> Response<ApiEnvelope<List<DateCheckinDto>>> =
        { refusedWithFixture(404, "date_feedback_post_404_not_enabled.json") }
    val dateFeedbackWrites = mutableListOf<Pair<String, DateFeedbackRequest>>()
    var dateFeedbackResponse: (matchId: String, DateFeedbackRequest) -> Response<ApiEnvelope<DateFeedbackDto>> =
        { _, _ -> ok(fixture("date_feedback_post_201.json", DateFeedbackDto.serializer())) }

    override suspend fun dateCheckins(): Response<ApiEnvelope<List<DateCheckinDto>>> {
        calls += "date-checkins"
        return dateCheckinsResponse()
    }

    override suspend fun dateFeedback(matchId: String, body: DateFeedbackRequest): Response<ApiEnvelope<DateFeedbackDto>> {
        calls += "date-feedback"
        dateFeedbackWrites += matchId to body
        return dateFeedbackResponse(matchId, body)
    }

    // ── Mechanic M19: past matches ──────────────────────────────────────────

    /** `GET /past-matches` (not the envelope). The default is the server's flag OFF, as the golden writes it. */
    var pastMatchesResponse: () -> Response<PastMatchesDto> = { rawRefusedWithFixture(404, "past_matches_get_404_not_enabled.json") }

    override suspend fun pastMatches(): Response<PastMatchesDto> {
        calls += "past-matches"
        return pastMatchesResponse()
    }

    // ── Mechanic M13: kind messages ─────────────────────────────────────────

    /** Every text `POST /kind-check` was asked about, in order. */
    val kindChecks = mutableListOf<String>()

    /** `POST /kind-check`. The default is the server's flag OFF, as the golden writes it. */
    var kindCheckResponse: (text: String) -> Response<ApiEnvelope<KindCheckDto>> =
        { refusedWithFixture(404, "kind_check_post_404_not_enabled.json") }
    val botheredWrites = mutableListOf<Pair<String, BotheredRequest>>()
    var botheredResponse: (matchId: String, BotheredRequest) -> Response<ApiEnvelope<BotheredDto>> =
        { matchId, body -> ok(BotheredDto(matchId = matchId, bothered = body.bothered, offerReport = body.bothered)) }

    /** `GET /comment-filter`. The default is the server's flag OFF. */
    var commentFilterResponse: () -> Response<ApiEnvelope<CommentFilterDto>> =
        { refusedWithFixture(404, "kind_check_post_404_not_enabled.json") }
    val commentFilterWrites = mutableListOf<CommentFilterRequest>()
    var commentFilterWriteResponse: (CommentFilterRequest) -> Response<ApiEnvelope<CommentFilterDto>> =
        { ok(CommentFilterDto(filterUnkind = it.filterUnkind, words = it.words)) }

    override suspend fun kindCheck(body: KindCheckRequest): Response<ApiEnvelope<KindCheckDto>> {
        calls += "kind-check"
        kindChecks += body.text
        return kindCheckResponse(body.text)
    }

    override suspend fun bothered(matchId: String, body: BotheredRequest): Response<ApiEnvelope<BotheredDto>> {
        calls += "bothered"
        botheredWrites += matchId to body
        return botheredResponse(matchId, body)
    }

    override suspend fun commentFilter(): Response<ApiEnvelope<CommentFilterDto>> {
        calls += "comment-filter"
        return commentFilterResponse()
    }

    override suspend fun updateCommentFilter(body: CommentFilterRequest): Response<ApiEnvelope<CommentFilterDto>> {
        calls += "comment-filter:write"
        commentFilterWrites += body
        return commentFilterWriteResponse(body)
    }

    // ── Mechanic M16: hide from people I know ───────────────────────────────

    /** `GET /hide-known`. The default is the server's flag OFF, as the golden writes it. */
    var hideKnownResponse: () -> Response<ApiEnvelope<HideKnownDto>> =
        { refusedWithFixture(404, "hide_known_get_404_not_enabled.json") }
    val hideKnownWrites = mutableListOf<HideKnownRequest>()
    var hideKnownWriteResponse: (HideKnownRequest) -> Response<ApiEnvelope<HideKnownDto>> =
        { ok(HideKnownDto(enabled = it.enabled, hiddenCount = if (it.enabled) 2 else 0)) }

    override suspend fun hideKnown(): Response<ApiEnvelope<HideKnownDto>> {
        calls += "hide-known"
        return hideKnownResponse()
    }

    override suspend fun updateHideKnown(body: HideKnownRequest): Response<ApiEnvelope<HideKnownDto>> {
        calls += "hide-known:write"
        hideKnownWrites += body
        return hideKnownWriteResponse(body)
    }

    // ── Mechanic M18: client config ─────────────────────────────────────────

    /** `GET /client-config`. The default is protection off, as the golden writes it. */
    var clientConfigResponse: () -> Response<ApiEnvelope<ClientConfigDto>> =
        { ok(fixture("client_config_get_200_off.json", ClientConfigDto.serializer())) }

    override suspend fun clientConfig(): Response<ApiEnvelope<ClientConfigDto>> {
        calls += "client-config"
        return clientConfigResponse()
    }

    override suspend fun block(body: BlockRequest): Response<ApiEnvelope<BlockedDto>> {
        blocks += body.targetUserId
        return blockResponse()
    }

    override suspend fun report(body: ReportRequest): Response<ApiEnvelope<ReportResultDto>> {
        reports += body
        return reportResponse(body)
    }

    override suspend fun panic(body: PanicRequest) = ok(PanicDto(recorded = true, incidentId = "incident-1", status = "open"))

    override suspend fun trustedContacts() = ok(TrustedContactsDto(items = trusted, max = 3))

    override suspend fun addTrustedContact(contactId: String, body: TrustedContactRequest) = ok(TrustedContactDto(contactId = contactId))

    override suspend fun removeTrustedContact(contactId: String): Response<ApiEnvelope<RemovedDto>> {
        calls += "trusted-remove:$contactId"
        trusted = trusted.filterNot { it.contactId == contactId }
        return ok(RemovedDto(removed = true))
    }

    override suspend fun shareLocation(body: ShareLocationRequest): Response<ApiEnvelope<ShareLocationDto>> {
        calls += "share"
        val id = "share-${myShares.size + 1}"
        myShares = myShares + MyLocationShareDto(
            shareId = id,
            userId = ME,
            recipientId = body.recipientId,
            recipientKind = "match",
            expiresAt = "later",
            recipient = people[body.recipientId],
        )
        return ok(ShareLocationDto(shareId = id, recipientId = body.recipientId, recipientKind = "match", expiresAt = "later"))
    }

    override suspend fun myLocationShares(): Response<ApiEnvelope<MyLocationSharesDto>> {
        calls += "my-shares"
        return ok(MyLocationSharesDto(items = myShares))
    }

    override suspend fun sharedWithMe(): Response<ApiEnvelope<SharedWithMeDto>> {
        calls += "shared-with-me"
        return ok(SharedWithMeDto(items = sharedWithMe))
    }

    override suspend fun stopShare(id: String): Response<ApiEnvelope<StopShareDto>> {
        stops += id
        myShares = myShares.filterNot { it.shareId == id }
        return stopShareResponse?.invoke(id) ?: ok(StopShareDto(stopped = true, shareId = id))
    }

    override suspend fun sharedLocation(id: String) = ok(SharedLocationDto(shareId = id, latitude = 17.4, longitude = 78.4))

    override suspend fun requestExport() = ok(DataExportDto(id = "export-1", status = "pending"))

    override suspend fun exports() = ok(listOf(DataExportDto(id = "export-1", status = "ready")))

    override suspend fun downloadExport(id: String): Response<ResponseBody> = Response.success("{}".toResponseBody(JSON_TYPE))

    override suspend fun premiumCatalogue() = ok(fixture("premium_catalogue_get_200.json", PremiumCatalogueDto.serializer()))

    override suspend fun purchase(body: PremiumPurchaseRequest): Response<ApiEnvelope<PremiumPurchaseResultDto>> {
        calls += "purchase"
        purchaseRequests += body
        return purchaseResponse(body)
    }

    override suspend fun purchasePayment(id: String): Response<ApiEnvelope<PremiumPaymentDto>> {
        paymentReads++
        val answer = if (paymentAnswers.size > 1) paymentAnswers.removeFirst() else paymentAnswers.firstOrNull()
        return answer?.invoke() ?: premiumPayment(id, "confirming")
    }

    override suspend fun premiumMe() = ok(PremiumMeDto())

    fun repository() = DatingRepository(this, testJson)
}

fun premiumPayment(purchaseId: String = "purchase-1", status: String, refund: String? = null) =
    ok(PremiumPaymentDto(purchaseId = purchaseId, product = "pass_30d", status = status, amountMinor = 39_900, currency = "INR", refundStatus = refund))

/** The device's stored "did this bother you?" answers, in memory; survives a new [DatingConversationKindness] like the real file does. */
class FakeKindAnswers : com.us.android.feature.dating.safety.KindAnswerStore {
    val stored = linkedMapOf<String, Boolean>()

    override suspend fun answer(messageId: String): Boolean? = stored[messageId]

    override suspend fun remember(messageId: String, bothered: Boolean) {
        stored[messageId] = bothered
    }

    override suspend fun clear() = stored.clear()
}

class FakeLocation(var permission: Boolean = true, var fix: Coordinates? = Coordinates(17.44, 78.35)) : CurrentLocationSource {
    override fun hasPermission(): Boolean = permission
    override suspend fun current(): Coordinates? = fix
    override suspend fun cityOf(coordinates: Coordinates): String? = "Hyderabad"
}

/** The coordinator with a launcher that must never be asked to open: the Premium screen only CONFIRMS. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(activity: Activity, attempt: PaymentAttempt, session: PaymentSession, onOutcome: (PaymentOutcome) -> Unit) =
            error("the Premium ViewModel must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)
