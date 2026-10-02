package com.us.android.feature.dating.network

import com.us.android.core.network.ApiEnvelope
import okhttp3.ResponseBody
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query
import retrofit2.http.Streaming

/**
 * dating-service's user routes, as registered in `internal/http/handler.go`
 * `RegisterRoutes`. Every call under `/v1/dating` is 404 at the gateway unless the
 * caller is on the pilot allowlist (see DatingError.NotAvailable).
 */
@Suppress("TooManyFunctions")
interface DatingApi {

    // Consent

    @GET("v1/dating/consents")
    suspend fun consents(): Response<ApiEnvelope<ConsentsDto>>

    @PUT("v1/dating/consents/{type}")
    suspend fun setConsent(@Path("type") type: String, @Body body: ConsentRequest): Response<ApiEnvelope<ConsentsDto>>

    // Profile

    /** 404 NOT_FOUND "profile not found" until the first upsert. */
    @GET("v1/dating/profile")
    suspend fun profile(): Response<ApiEnvelope<DatingProfileDto>>

    @POST("v1/dating/profile")
    suspend fun upsertProfile(@Body body: UpsertProfileRequest): Response<ApiEnvelope<DatingProfileDto>>

    /** `{paused:true}` pauses; `{paused:false}` restores the remembered step but never lifts a hold. */
    @POST("v1/dating/profile/pause")
    suspend fun pause(@Body body: PauseRequest): Response<ApiEnvelope<DatingProfileDto>>

    @HTTP(method = "DELETE", path = "v1/dating/profile", hasBody = true)
    suspend fun deleteProfile(@Body body: DeleteProfileRequest): Response<ApiEnvelope<StatusDto>>

    @GET("v1/dating/profile/privacy")
    suspend fun privacy(): Response<ApiEnvelope<PrivacyDto>>

    @PATCH("v1/dating/profile/privacy")
    suspend fun updatePrivacy(@Body body: PrivacyUpdateRequest): Response<ApiEnvelope<PrivacyDto>>

    /** With the M6 filters flag on, carries `distance_bucket` and `pass_filters`; without it, neither. */
    @GET("v1/dating/preferences")
    suspend fun preferences(): Response<ApiEnvelope<PreferencesDto>>

    /**
     * 403 FILTERS_REQUIRE_PASS for a pass filter (or the old `language_filter`)
     * set without a pass while the M6 flag is on; 400 INVALID_DISTANCE_BUCKET,
     * INVALID_HEIGHT, INVALID_LANGUAGE, INVALID_LIFESTYLE with `details.field`.
     */
    @PUT("v1/dating/preferences")
    suspend fun updatePreferences(@Body body: PreferencesRequest): Response<ApiEnvelope<PreferencesDto>>

    /** Mechanic M6: the fixed interest, language, height, basics and distance lists, with our labels. */
    @GET("v1/dating/profile/options")
    suspend fun profileOptions(): Response<ApiEnvelope<ProfileOptionsDto>>

    // Photos

    @GET("v1/dating/photos/me")
    suspend fun myPhotos(): Response<ApiEnvelope<List<DatingPhotoDto>>>

    /** 201 with the photo already classified: approved, pending_review or rejected. */
    @POST("v1/dating/photos")
    suspend fun attachPhoto(@Body body: AttachPhotoRequest): Response<ApiEnvelope<DatingPhotoDto>>

    @PATCH("v1/dating/photos/{id}")
    suspend fun updatePhoto(@Path("id") id: String, @Body body: UpdatePhotoRequest): Response<ApiEnvelope<DatingPhotoDto>>

    @DELETE("v1/dating/photos/{id}")
    suspend fun deletePhoto(@Path("id") id: String): Response<ApiEnvelope<StatusDto>>

    // Prompts

    @GET("v1/dating/prompts/catalog")
    suspend fun promptCatalog(): Response<ApiEnvelope<List<PromptCatalogItemDto>>>

    /** `data` is null (not []) when there are no answers. */
    @GET("v1/dating/prompts")
    suspend fun prompts(): Response<ApiEnvelope<List<PromptAnswerDto>>>

    @PUT("v1/dating/prompts/{promptId}")
    suspend fun answerPrompt(@Path("promptId") promptId: Int, @Body body: PromptAnswerRequest): Response<ApiEnvelope<PromptAnswerDto>>

    @DELETE("v1/dating/prompts/{promptId}")
    suspend fun deletePrompt(@Path("promptId") promptId: Int): Response<ApiEnvelope<StatusDto>>

    // Selfie liveness

    @POST("v1/dating/verification/selfie/challenge")
    suspend fun selfieChallenge(): Response<ApiEnvelope<SelfieChallengeDto>>

    /** 409 MEDIA_NOT_READY while the clip is still processing — the attempt is NOT spent. */
    @POST("v1/dating/verification/selfie")
    suspend fun submitSelfie(@Body body: SelfieSubmitRequest): Response<ApiEnvelope<SelfieResultDto>>

    /** Where the face check stands: state, attempts left today, and the next step. */
    @GET("v1/dating/verification/status")
    suspend fun verificationStatus(): Response<ApiEnvelope<VerificationStatusDto>>

    // People

    /** The compact card. 404 without a match, a live incoming spark or deck membership. */
    @GET("v1/dating/people/{userId}")
    suspend fun person(@Path("userId") userId: String): Response<ApiEnvelope<DatingPersonDto>>

    // Pulse

    /** NOT wrapped in the envelope: `{data:[...], meta:{generated_at,size}, cohort_gated?}`. */
    @GET("v1/dating/pulse/today")
    suspend fun pulseToday(): Response<PulseTodayDto>

    @GET("v1/dating/pulse/{targetUserId}/explain")
    suspend fun explain(@Path("targetUserId") targetUserId: String): Response<ApiEnvelope<ExplainDto>>

    @POST("v1/dating/pulse/{candidateId}/pass")
    suspend fun pass(@Path("candidateId") candidateId: String, @Body body: PassRequest): Response<ApiEnvelope<PassDto>>

    /**
     * Mechanic M2: undoes the caller's most recent pass, one step, never after a
     * spark. No body. 409 REWIND_NOTHING_TO_UNDO, 429 REWIND_LIMIT_REACHED,
     * 404 CANDIDATE_UNAVAILABLE, 404 MECHANIC_NOT_ENABLED.
     */
    @POST("v1/dating/pulse/rewind")
    suspend fun rewind(): Response<ApiEnvelope<RewindDto>>

    /** Mechanic M10: every daily allowance; a mechanic whose flag is off is absent. */
    @GET("v1/dating/allowances")
    suspend fun allowances(): Response<ApiEnvelope<AllowancesDto>>

    // Sparks

    @POST("v1/dating/sparks")
    suspend fun spark(@Body body: SparkRequest): Response<ApiEnvelope<SparkCreatedDto>>

    @GET("v1/dating/sparks/incoming")
    suspend fun incomingSparks(@Query("limit") limit: Int = 50): Response<ApiEnvelope<List<SparkDto>>>

    /**
     * Mechanic M4: who sparked the caller, as a grid, with the total. Locked
     * (the gate on, no pass): no names, ids, notes or full images.
     */
    @GET("v1/dating/liked-you")
    suspend fun likedYou(
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
    ): Response<ApiEnvelope<LikedYouDto>>

    @POST("v1/dating/sparks/{id}/decline")
    suspend fun declineSpark(@Path("id") id: String): Response<ApiEnvelope<SparkDeclineDto>>

    /**
     * Accepts an incoming spark: sparks back through the normal path. Idempotent;
     * 404 once declined; 403 LIKED_YOU_LOCKED on a locked spark (mechanic M4).
     */
    @POST("v1/dating/sparks/{id}/accept")
    suspend fun acceptSpark(@Path("id") id: String): Response<ApiEnvelope<SparkCreatedDto>>

    // Stash

    @POST("v1/dating/stash")
    suspend fun stash(@Body body: StashRequest): Response<ApiEnvelope<StashDto>>

    @GET("v1/dating/stash")
    suspend fun stashed(): Response<ApiEnvelope<List<StashDto>>>

    @DELETE("v1/dating/stash/{candidateId}")
    suspend fun unstash(@Path("candidateId") candidateId: String): Response<ApiEnvelope<RemovedDto>>

    // Matches

    @GET("v1/dating/matches")
    suspend fun matches(@Query("status") status: String? = null): Response<ApiEnvelope<List<MatchDto>>>

    @GET("v1/dating/matches/{id}")
    suspend fun match(@Path("id") id: String): Response<ApiEnvelope<MatchDto>>

    /** Unmatch. */
    @POST("v1/dating/matches/{id}/close")
    suspend fun closeMatch(@Path("id") id: String): Response<ApiEnvelope<ClosedDto>>

    /**
     * More time on a match. Free once per rolling 24 h for the person waiting
     * on a first-move match (429 EXTEND_LIMIT_REACHED after that); otherwise
     * the premium 7-day extend (403 FORBIDDEN without a pass). No body.
     */
    @POST("v1/dating/matches/{id}/extend")
    suspend fun extendMatch(@Path("id") id: String): Response<ApiEnvelope<ExtendDto>>

    /**
     * Mechanic M5: the waiting person answers one of the first mover's
     * opening questions; the server posts it as the chat's first message.
     * 409 FIRST_MOVE_NOT_PENDING, 404 OPENING_QUESTION_UNKNOWN,
     * 400 OPENING_ANSWER_INVALID / OPENING_ANSWER_REFUSED, 503 CHAT_UNAVAILABLE.
     */
    @POST("v1/dating/matches/{id}/opening-answer")
    suspend fun openingAnswer(@Path("id") id: String, @Body body: OpeningAnswerRequest): Response<ApiEnvelope<OpeningAnswerDto>>

    // First move (mechanic M5)

    /** 404 MECHANIC_NOT_ENABLED while the server flag is off. */
    @GET("v1/dating/first-move")
    suspend fun firstMove(): Response<ApiEnvelope<FirstMoveSettingsDto>>

    /** 400 OPENING_QUESTIONS_TOO_MANY / OPENING_QUESTION_INVALID / OPENING_QUESTION_REFUSED. */
    @PUT("v1/dating/first-move")
    suspend fun updateFirstMove(@Body body: FirstMoveRequest): Response<ApiEnvelope<FirstMoveSettingsDto>>

    // Safety

    @POST("v1/dating/safety/block")
    suspend fun block(@Body body: BlockRequest): Response<ApiEnvelope<BlockedDto>>

    /** Who I have blocked. */
    @GET("v1/dating/blocks")
    suspend fun blocks(): Response<ApiEnvelope<BlocksDto>>

    /** Lifts a block. Idempotent, and restores NOTHING: no match, no spark, no conversation. */
    @DELETE("v1/dating/blocks/{userId}")
    suspend fun unblock(@Path("userId") userId: String): Response<ApiEnvelope<UnblockedDto>>

    /** 201; the reporter automatically blocks the target (`blocked: true`). */
    @POST("v1/dating/safety/report")
    suspend fun report(@Body body: ReportRequest): Response<ApiEnvelope<ReportResultDto>>

    @POST("v1/dating/safety/panic")
    suspend fun panic(@Body body: PanicRequest): Response<ApiEnvelope<PanicDto>>

    @GET("v1/dating/safety/trusted-contacts")
    suspend fun trustedContacts(): Response<ApiEnvelope<TrustedContactsDto>>

    @PUT("v1/dating/safety/trusted-contacts/{contactId}")
    suspend fun addTrustedContact(
        @Path("contactId") contactId: String,
        @Body body: TrustedContactRequest,
    ): Response<ApiEnvelope<TrustedContactDto>>

    @DELETE("v1/dating/safety/trusted-contacts/{contactId}")
    suspend fun removeTrustedContact(@Path("contactId") contactId: String): Response<ApiEnvelope<RemovedDto>>

    @POST("v1/dating/safety/share-location")
    suspend fun shareLocation(@Body body: ShareLocationRequest): Response<ApiEnvelope<ShareLocationDto>>

    /** My own live shares, so Stop works in a process that did not start them. No coordinates. */
    @GET("v1/dating/safety/share-location")
    suspend fun myLocationShares(): Response<ApiEnvelope<MyLocationSharesDto>>

    /** Shares sent TO me. No coordinates: those come from [sharedLocation]. */
    @GET("v1/dating/safety/shared-locations")
    suspend fun sharedWithMe(): Response<ApiEnvelope<SharedWithMeDto>>

    @DELETE("v1/dating/safety/share-location/{id}")
    suspend fun stopShare(@Path("id") id: String): Response<ApiEnvelope<StopShareDto>>

    /** Recipient only, while live; 404 once expired, stopped or not yours. */
    @GET("v1/dating/safety/shared-locations/{id}")
    suspend fun sharedLocation(@Path("id") id: String): Response<ApiEnvelope<SharedLocationDto>>

    // Data rights

    /** 202; an export already pending or processing is returned instead of a new one. */
    @POST("v1/dating/data-export")
    suspend fun requestExport(): Response<ApiEnvelope<DataExportDto>>

    @GET("v1/dating/data-export/me")
    suspend fun exports(): Response<ApiEnvelope<List<DataExportDto>>>

    /** Owner only. Raw JSON bytes, not the envelope, `Cache-Control: no-store`. */
    @Streaming
    @GET("v1/dating/data-export/{id}/download")
    suspend fun downloadExport(@Path("id") id: String): Response<ResponseBody>

    // Premium

    @GET("v1/dating/premium/catalogue")
    suspend fun premiumCatalogue(): Response<ApiEnvelope<PremiumCatalogueDto>>

    /** 201 new, 200 repeat of the same key; `client_session` only while payable. */
    @POST("v1/dating/premium/purchases")
    suspend fun purchase(@Body body: PremiumPurchaseRequest): Response<ApiEnvelope<PremiumPurchaseResultDto>>

    /** The poll: `confirming | paid | failed` plus `refund_status`. Paid only from the signed payments event. */
    @GET("v1/dating/premium/purchases/{id}/payment")
    suspend fun purchasePayment(@Path("id") id: String): Response<ApiEnvelope<PremiumPaymentDto>>

    @GET("v1/dating/premium/me")
    suspend fun premiumMe(): Response<ApiEnvelope<PremiumMeDto>>
}
