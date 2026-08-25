import XCTest
@testable import UsModel

final class FeedItemTests: XCTestCase {
    func testFeedItemJSONDecoding() throws {
        let json = """
        {
            "id": "c1604d02-a4fe-44f2-91a6-feebd0ac814f",
            "author_id": "71851843-a69f-4d2f-a2f8-9f6eea629609",
            "text": "Evidence pass fixture 3",
            "post_type": "text",
            "is_pinned": false,
            "created_at": "2026-08-17T10:16:51.278089Z",
            "updated_at": "2026-08-17T10:16:51.278089Z",
            "counts": {
                "likes": 5,
                "comments": 2,
                "reposts": 1
            },
            "view_count": 42,
            "has_reacted": true,
            "is_bookmarked": false,
            "repost_count": 1,
            "has_reposted": false,
            "is_repostable": true,
            "author": {
                "id": "71851843-a69f-4d2f-a2f8-9f6eea629609",
                "display_name": "iOS Evidence"
            }
        }
        """.data(using: .utf8)!

        let decoder = JSONDecoder()
        let item = try decoder.decode(FeedItem.self, from: json)

        XCTAssertEqual(item.id, "c1604d02-a4fe-44f2-91a6-feebd0ac814f")
        XCTAssertEqual(item.author.nameForDisplay, "iOS Evidence")
        XCTAssertEqual(item.counts.likes, 5)
        XCTAssertEqual(item.counts.comments, 2)
        XCTAssertEqual(item.counts.reposts, 1)
        XCTAssertTrue(item.viewer.hasReacted)
        XCTAssertFalse(item.viewer.isBookmarked)
    }

    func testEngagementOverlayCalculations() {
        var overlay = EngagementOverlay()
        XCTAssertEqual(overlay.likeCountOr(10, serverReacted: false), 10)

        // Optimistically like
        overlay.hasReacted = true
        XCTAssertEqual(overlay.likeCountOr(10, serverReacted: false), 11)
        XCTAssertTrue(overlay.reactedOr(false))

        // Optimistically unlike
        overlay.hasReacted = false
        XCTAssertEqual(overlay.likeCountOr(10, serverReacted: true), 9)
        XCTAssertFalse(overlay.reactedOr(true))
    }
}
