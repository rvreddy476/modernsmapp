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
import com.us.android.feature.dating.network.BlockRequest
import com.us.android.feature.dating.network.BlockedDto
import com.us.android.feature.dating.network.BlockedPersonDto
import com.us.android.feature.dating.network.BlocksDto
import com.us.android.feature.dating.network.ClosedDto
import com.us.android.feature.dating.network.ConsentRequest
import com.us.android.feature.dating.network.ConsentStateDto
import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.AttachPhotoRequest
import com.us.android.feature.dating.network.DataExportDto
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.DatingPhotoDto
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.DeleteProfileRequest
import com.us.android.feature.dating.network.ExplainDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.MyLocationShareDto
import com.us.android.feature.dating.network.MyLocationSharesDto
import com.us.android.feature.dating.network.PanicDto
import com.us.android.feature.dating.network.PanicRequest
import com.us.android.feature.dating.network.PassDto
import com.us.android.feature.dating.network.PassRequest
import com.us.android.feature.dating.network.PauseRequest
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
import com.us.android.feature.dating.network.CardPhotoDto
import com.us.android.feature.dating.network.DetailPromptDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.network.PulseCardDto
import com.us.android.feature.dating.network.PulseProfileDto
import com.us.android.feature.dating.network.PulseTodayDto
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
) = PulseCardDto(
    candidateId = userId,
    profile = PulseProfileDto(
        userId = userId,
        firstName = "Person $userId",
        age = 30,
        city = "Hyderabad",
        distanceBucket = bucket,
        distanceLabel = label,
        primaryPhotoUrl = "/v1/dating/photos/photo-$userId/full",
        trustTier = "selfie",
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
    detail = detail,
)

fun match(id: String, other: String, card: DatingPersonDto? = person(other)) =
    MatchDto(id = id, userA = ME, userB = other, status = "matched", conversationId = "conv-$id", person = card)

fun spark(id: String, from: String, card: DatingPersonDto? = person(from)) =
    SparkDto(id = id, fromUserId = from, toUserId = ME, targetKind = "photo", targetRef = "0", person = card)

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
    val passes = mutableListOf<String>()
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

    override suspend fun privacy() = ok(PrivacyDto())

    override suspend fun updatePrivacy(body: PrivacyUpdateRequest) = ok(PrivacyDto(incognito = body.incognito ?: false))

    override suspend fun preferences(): Response<ApiEnvelope<PreferencesDto>> {
        calls += "preferences"
        return ok(preferences)
    }

    override suspend fun updatePreferences(body: PreferencesRequest): Response<ApiEnvelope<PreferencesDto>> {
        calls += "preferences:write"
        preferenceWrites += body
        return preferencesWriteResponse?.invoke(body) ?: ok(preferences.copy(interestedInGender = body.interestedInGender))
    }

    override suspend fun myPhotos(): Response<ApiEnvelope<List<DatingPhotoDto>>> = ok(emptyList())

    override suspend fun attachPhoto(body: AttachPhotoRequest) = ok(DatingPhotoDto(id = "photo-1", mediaId = body.mediaId, moderationStatus = "approved"))

    override suspend fun updatePhoto(id: String, body: UpdatePhotoRequest) = ok(DatingPhotoDto(id = id))

    override suspend fun deletePhoto(id: String) = ok(StatusDto("deleted"))

    override suspend fun promptCatalog() = ok(listOf(PromptCatalogItemDto(1, "My ideal Sunday is...")))

    override suspend fun prompts(): Response<ApiEnvelope<List<PromptAnswerDto>>> = Response.success(ApiEnvelope(data = null))

    override suspend fun answerPrompt(promptId: Int, body: PromptAnswerRequest) = ok(PromptAnswerDto(promptId = promptId, answer = body.answer))

    override suspend fun deletePrompt(promptId: Int) = ok(StatusDto("deleted"))

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

    override suspend fun pass(candidateId: String, body: PassRequest): Response<ApiEnvelope<PassDto>> {
        passes += candidateId
        return ok(PassDto(passed = true, candidateId = candidateId))
    }

    override suspend fun spark(body: SparkRequest): Response<ApiEnvelope<SparkCreatedDto>> {
        calls += "spark"
        sparks += body
        return sparkResponse(body)
    }

    override suspend fun incomingSparks(limit: Int): Response<ApiEnvelope<List<SparkDto>>> {
        calls += "incoming"
        return ok(incoming)
    }

    override suspend fun declineSpark(id: String): Response<ApiEnvelope<SparkDeclineDto>> {
        declines += id
        return ok(SparkDeclineDto(declined = true, sparkId = id))
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

    override suspend fun matches(status: String?): Response<ApiEnvelope<List<MatchDto>>> {
        calls += "matches"
        return ok(matches)
    }

    override suspend fun match(id: String): Response<ApiEnvelope<MatchDto>> =
        matches.firstOrNull { it.id == id }?.let { ok(it) } ?: refused(404, "NOT_FOUND")

    override suspend fun closeMatch(id: String) = ok(ClosedDto(closed = true))

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
