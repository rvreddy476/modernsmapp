package com.us.android.feature.rider.onboarding

/** How the profile's free-text vehicle type reads (food-service riderkyc.ClassifyVehicle). */
enum class VehicleClass { UNKNOWN, BICYCLE, MOTORISED }

data class VehicleChoice(val wire: String, val label: String)

/**
 * MIRRORS riderkyc.ClassifyVehicle / DrivingDocumentsRequired, with its test
 * vectors. "BIKE" is a motorbike (Indian usage); an electric bike is motorised.
 * An unknown type needs the driving documents: the rule fails closed.
 */
object Vehicle {
    private val BICYCLE = setOf("BICYCLE", "CYCLE", "PEDAL_CYCLE")
    private val MOTORISED = setOf(
        "MOTORCYCLE", "MOTORBIKE", "BIKE", "SCOOTER", "SCOOTY", "MOPED",
        "EV_SCOOTER", "ELECTRIC_SCOOTER", "EV_BIKE", "ELECTRIC_BIKE",
    )

    fun classify(vehicleType: String): VehicleClass {
        val v = vehicleType.trim().uppercase().replace(' ', '_').replace('-', '_')
        return when (v) {
            in BICYCLE -> VehicleClass.BICYCLE
            in MOTORISED -> VehicleClass.MOTORISED
            else -> VehicleClass.UNKNOWN
        }
    }

    /** False only for a bicycle: a bicycle rider owes no driving licence and no RC. */
    fun drivingDocumentsRequired(vehicleType: String): Boolean = classify(vehicleType) != VehicleClass.BICYCLE

    /** What the profile form offers. Every wire value classifies as known. */
    val CHOICES: List<VehicleChoice> = listOf(
        VehicleChoice("MOTORCYCLE", "Motorcycle"),
        VehicleChoice("SCOOTER", "Scooter"),
        VehicleChoice("EV_SCOOTER", "Electric scooter"),
        VehicleChoice("BICYCLE", "Bicycle"),
    )
}

/** The rider verification steps, in the order food-service presents them (riderkyc Step*). */
enum class RiderStep(val wire: String, val title: String) {
    VEHICLE("vehicle", "Your vehicle"),
    AADHAAR_DIGILOCKER("aadhaar_digilocker", "Aadhaar via DigiLocker"),
    DRIVING_LICENCE("driving_licence", "Driving licence"),
    VEHICLE_RC("vehicle_rc", "Vehicle registration (RC)"),
    SELFIE("selfie", "Selfie"),
    PAYOUT_ACCOUNT("payout_account", "Bank account"),
    ;

    companion object {
        fun fromWire(code: String): RiderStep? = entries.firstOrNull { it.wire == code }
    }
}

enum class RowStatus { DONE, TO_DO, NOT_NEEDED }

/**
 * The rider's checklist from the server's `missing[]` — the only source of truth
 * for readiness; the client never marks a step done from its own state.
 *
 * DL and RC rows read NOT_NEEDED when the vehicle needs no driving documents
 * (a bicycle), mirroring riderkyc.MissingSteps, which never lists them then.
 * If the server lists them anyway, they are TO_DO: the server wins.
 *
 * Fails closed: an unknown step code keeps the checklist not ready.
 */
data class RiderChecklist(
    val rows: List<Row>,
    val unrecognised: List<String>,
) {
    data class Row(val step: RiderStep, val status: RowStatus)

    val isReady: Boolean get() = unrecognised.isEmpty() && rows.none { it.status == RowStatus.TO_DO }

    val nextStep: RiderStep? get() = rows.firstOrNull { it.status == RowStatus.TO_DO }?.step

    companion object {
        private val DRIVING_STEPS = setOf(RiderStep.DRIVING_LICENCE, RiderStep.VEHICLE_RC)

        fun from(missing: List<String>, drivingDocumentsRequired: Boolean): RiderChecklist {
            val owed = missing.mapNotNull(RiderStep::fromWire).toSet()
            return RiderChecklist(
                rows = RiderStep.entries.map { step ->
                    val status = when {
                        step in owed -> RowStatus.TO_DO
                        step in DRIVING_STEPS && !drivingDocumentsRequired -> RowStatus.NOT_NEEDED
                        else -> RowStatus.DONE
                    }
                    Row(step, status)
                },
                unrecognised = missing.filter { RiderStep.fromWire(it) == null }.distinct(),
            )
        }

        /** The checklist for a vehicle type as the profile states it. */
        fun forVehicle(missing: List<String>, vehicleType: String): RiderChecklist =
            from(missing, Vehicle.drivingDocumentsRequired(vehicleType))
    }
}
