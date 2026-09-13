package com.us.android.feature.rider

import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import java.io.File

/**
 * The food-service golden fixtures, read in place from :core:food (one copy of
 * each; :core:food proves they are byte-identical to the goldens) and decoded
 * STRICTLY, the way the rider screens will meet them.
 */
internal object RiderFixtures {
    private val strict = Json { ignoreUnknownKeys = false }
    private val coreFood = File("../../core/food/src/test/resources/contracts")
    private val status = Regex("""_(\d{3})(?:_|\.json)""")

    fun <T> success(name: String, serializer: KSerializer<T>): FoodResult.Success<T> {
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), File(coreFood, name).readText())
        return FoodResult.Success(checkNotNull(envelope.data) { "$name carried no data" })
    }

    fun failure(name: String): FoodResult.Failure {
        val code = checkNotNull(status.find(name)) { "$name carries no HTTP status" }.groupValues[1].toInt()
        val envelope = strict.decodeFromString(FoodErrorEnvelopeDto.serializer(), File(coreFood, name).readText())
        return FoodResult.Failure(FoodError.from(code, checkNotNull(envelope.error)))
    }

    fun names(prefix: String): List<String> =
        coreFood.listFiles { f -> f.name.startsWith(prefix) && f.name.endsWith(".json") }.orEmpty().map { it.name }.sorted()
}
