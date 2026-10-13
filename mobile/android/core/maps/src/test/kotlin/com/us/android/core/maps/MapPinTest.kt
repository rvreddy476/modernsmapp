package com.us.android.core.maps

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MapPinTest {
    @Test fun `rejects unusable map coordinates`() {
        assertFalse(MapPin(Double.NaN, 78.0, "A").valid)
        assertFalse(MapPin(17.0, Double.POSITIVE_INFINITY, "A").valid)
        assertFalse(MapPin(91.0, 78.0, "A").valid)
        assertFalse(MapPin(17.0, -181.0, "A").valid)
        assertTrue(MapPin(17.4401, 78.3489, "A").valid)
        assertTrue(MapPin(0.0, 0.0, "Equator").valid)
    }
}
