package com.us.android.core.database

/**
 * The state and payload invariants SQLite `CHECK` would carry, enforced at the
 * one place every write goes through.
 *
 * ## WHY NOT `CHECK` CONSTRAINTS
 *
 * Room generates the fresh schema from the `@Entity` declarations, and `@Entity`
 * has no way to declare a `CHECK`. Writing them only in the migration is exactly
 * the defect that made the migrated and fresh databases disagree — Room compares
 * the two schemas on open and rejects the mismatch, so the upgrade path would
 * simply fail to start.
 *
 * These are therefore *modelled* invariants: the DAO's guarded transactions are
 * the only way in, and they call these first. That is an implementation
 * mechanism, not a change to what is guaranteed — every rule below is one the
 * frozen specification already required.
 *
 * They throw rather than return, because a caller that constructs one of these
 * rows wrongly has a bug, and persisting it would corrupt the exact records that
 * decide whether a post is published twice.
 */
object CreatorInvariants {

    /** Terminal states hold no live slot. */
    val TERMINAL_OPERATION_STATES = setOf("published", "superseded")

    /** States that occupy the one live slot per project. */
    val LIVE_OPERATION_STATES = setOf("publishing", "failed")

    private val ALL_OPERATION_STATES = LIVE_OPERATION_STATES + TERMINAL_OPERATION_STATES

    private val SHA256 = Regex("^[0-9a-f]{64}$")

    /**
     * A publish operation is well formed.
     *
     * The frozen-request rules are the load-bearing ones. A zero or negative
     * byte count, or a hash that is not a hash, means the record cannot be used
     * to verify a replay — and an unverifiable replay is how the same
     * idempotency key gets sent with different bytes.
     */
    fun requireValidOperation(operation: CreatorPublishOperationEntity) {
        require(operation.state in ALL_OPERATION_STATES) {
            "unknown publish state '${operation.state}'; " +
                "an unrecognised state would sit outside every liveness rule"
        }
        // R-3: a positive frozen length. Zero bytes cannot be the request the
        // server hashed, so a row claiming it is unretryable by definition.
        require(operation.frozenRequestBytes > 0) {
            "frozenRequestBytes must be positive, was ${operation.frozenRequestBytes}"
        }
        require(SHA256.matches(operation.frozenRequestSha256)) {
            "frozenRequestSha256 is not a sha256"
        }
        require(operation.creationKey.isNotBlank()) { "creationKey must not be blank" }
        require(operation.frozenRequestBase64.isNotBlank()) {
            "frozenRequestBase64 must not be blank"
        }
        // Only a published operation names a server post; only a superseded one
        // names its successor. Anything else is a state that contradicts itself.
        if (operation.state == "published") {
            require(!operation.serverPostId.isNullOrBlank()) {
                "a published operation must record its server post id"
            }
        } else {
            require(operation.serverPostId == null) {
                "only a published operation may carry a server post id"
            }
        }
        if (operation.state == "superseded") {
            require(!operation.supersededByOperationId.isNullOrBlank()) {
                "a superseded operation must name its successor"
            }
        } else {
            require(operation.supersededByOperationId == null) {
                "only a superseded operation may name a successor"
            }
        }
    }

    /** The fallback singleton's state and reason must agree, in both directions. */
    fun requireValidFallbackState(state: ComposerDraftFallbackStateEntity) {
        require(state.id == ComposerDraftFallbackStateEntity.SINGLETON_ID) {
            "the fallback state is a singleton; id must be " +
                ComposerDraftFallbackStateEntity.SINGLETON_ID
        }
        when (state.state) {
            ComposerDraftFallbackStateEntity.AVAILABLE ->
                require(state.reason == null) {
                    "an AVAILABLE fallback carries no reason"
                }
            ComposerDraftFallbackStateEntity.UNAVAILABLE ->
                require(state.reason in VALID_UNAVAILABLE_REASONS) {
                    "UNAVAILABLE needs a known reason, got '${state.reason}'"
                }
            else -> throw IllegalArgumentException("unknown fallback state '${state.state}'")
        }
    }

    /**
     * A typed recovery row carries exactly what its kind promises.
     *
     * `RETRYABLE_PUBLISH` is the strict one: without all five fields there is
     * nothing to verify the replay against, and a replay that cannot be verified
     * must never be sent.
     */
    fun requireValidRecovery(recovery: CreatorLegacyRecoveryEntity) {
        require((recovery.creationKey == null) == (recovery.frozenRequestJson == null)) {
            "creationKey and frozenRequestJson are both-or-neither"
        }
        when (recovery.kind) {
            CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH -> {
                require(recovery.creationKey != null) { "RETRYABLE_PUBLISH needs a creation key" }
                require(recovery.frozenRequestJson != null) { "RETRYABLE_PUBLISH needs frozen bytes" }
                require(recovery.frozenRequestSha != null) { "RETRYABLE_PUBLISH needs a frozen sha" }
                require((recovery.frozenRequestLen ?: 0) > 0) {
                    "RETRYABLE_PUBLISH needs a positive frozen length"
                }
                // media_id is deliberately OPTIONAL here — approval R-3. A legacy
                // TEXT publish froze a request with no media at all, and refusing
                // to retry it would strand a post that may already have committed.
            }
            CreatorLegacyRecoveryEntity.KIND_TEXT_ONLY -> {
                require(recovery.mediaId == null) { "TEXT_ONLY carries no media id" }
                require(recovery.creationKey == null) { "TEXT_ONLY carries no operation" }
                require(recovery.text.isNotBlank()) { "TEXT_ONLY needs text worth recovering" }
            }
            CreatorLegacyRecoveryEntity.KIND_UNUSABLE -> {
                require(recovery.creationKey == null) { "UNUSABLE carries no frozen operation" }
                require(recovery.frozenRequestJson == null) { "UNUSABLE carries no frozen bytes" }
            }
            else -> throw IllegalArgumentException("unknown recovery kind '${recovery.kind}'")
        }
    }

    private val VALID_UNAVAILABLE_REASONS = setOf(
        ComposerDraftFallbackStateEntity.REASON_UNREPRESENTABLE,
        ComposerDraftFallbackStateEntity.REASON_QUARANTINED,
        ComposerDraftFallbackStateEntity.REASON_RECOVERY_REQUIRED,
    )
}

/**
 * A guarded write did not affect the rows it named.
 *
 * Thrown inside a `@Transaction`, which rolls the whole thing back. That is the
 * point: a cleanup that names a recovery row which does not exist must not go on
 * to delete the user's retained legacy draft.
 */
class UnexpectedRowCountException(
    operation: String,
    expected: Int,
    actual: Int,
) : IllegalStateException(
    "$operation affected $actual rows, expected $expected; " +
        "rolling back rather than continuing on rows that are not there",
)

/**
 * A guarded mutation must have affected exactly the rows it named.
 *
 * Extracted so the cleanup transaction reads as a sequence of proofs rather than
 * a stack of `if (…) throw`. Each call is a claim: "this statement changed the
 * row I said it would". The first one that is not true rolls the transaction
 * back before anything destructive runs.
 */
internal fun requireAffected(operation: String, actual: Int, expected: Int = 1) {
    if (actual != expected) throw UnexpectedRowCountException(operation, expected, actual)
}
