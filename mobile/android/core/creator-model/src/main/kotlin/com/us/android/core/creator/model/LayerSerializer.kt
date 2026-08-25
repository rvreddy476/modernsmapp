package com.us.android.core.creator.model

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.descriptors.buildClassSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * Dispatches [Layer] on its own `type` field.
 *
 * kotlinx's built-in polymorphism would write an extra discriminator key
 * (`"type"` by default) ALONGSIDE the declared `type` property, producing a
 * duplicate key and different bytes than the frozen fixtures. The contract
 * already has a discriminator; this serializer uses that one instead of adding
 * a second.
 */
object LayerSerializer : KSerializer<Layer> {

    override val descriptor: SerialDescriptor = buildClassSerialDescriptor("Layer")

    override fun serialize(encoder: Encoder, value: Layer) {
        val jsonEncoder = encoder as? JsonEncoder
            ?: throw SerializationException("Layer is JSON-only; the contract is a JSON document")
        val element = when (value) {
            is ImageLayer -> jsonEncoder.json.encodeToJsonElement(ImageLayer.serializer(), value)
            is TextLayer -> jsonEncoder.json.encodeToJsonElement(TextLayer.serializer(), value)
        }
        jsonEncoder.encodeJsonElement(element)
    }

    override fun deserialize(decoder: Decoder): Layer {
        val jsonDecoder = decoder as? JsonDecoder
            ?: throw SerializationException("Layer is JSON-only; the contract is a JSON document")
        val obj: JsonObject = jsonDecoder.decodeJsonElement().jsonObject
        return when (val type = obj["type"]?.jsonPrimitive?.content) {
            Layer.TYPE_IMAGE -> jsonDecoder.json.decodeFromJsonElement(ImageLayer.serializer(), obj)
            Layer.TYPE_TEXT -> jsonDecoder.json.decodeFromJsonElement(TextLayer.serializer(), obj)
            // Not "ignore and continue": an unknown layer type means the
            // document was written by something this build does not understand,
            // and rendering it minus a layer would silently alter the artwork.
            else -> throw SerializationException("unknown layer type '$type'")
        }
    }
}
