// Affiliate-redirect landing screen.
//
// Mobile equivalent of the commerce-service GET /v1/commerce/affiliate/
// :linkId server-side 302. The viewer tapped an in-video product
// overlay; we land here, hit the server endpoint with redirect-
// following DISABLED (we want to read the Location header, not
// blindly follow it inside Dio), capture the affiliate code into
// AffiliateAttribution, then push the canonical product detail
// screen.
//
// Why we don't just follow the server redirect transparently:
//   Dio follows redirects to fetch the body — that round-trips to
//   /v1/commerce/products/:id which we'd then have to parse + map to
//   the in-app product page. Reading the Location header gives us
//   the product id directly without a second HTTP call.

import 'package:atpost_app/services/affiliate_attribution.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class AffiliateRedirectScreen extends ConsumerStatefulWidget {
  const AffiliateRedirectScreen({super.key, required this.linkId});

  final String linkId;

  @override
  ConsumerState<AffiliateRedirectScreen> createState() =>
      _AffiliateRedirectScreenState();
}

class _AffiliateRedirectScreenState
    extends ConsumerState<AffiliateRedirectScreen> {
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _resolve());
  }

  Future<void> _resolve() async {
    final api = ref.read(apiClientProvider);
    try {
      // followRedirects=false lets us read the Location header instead
      // of round-tripping through the product detail and parsing the
      // body. validateStatus accepts 302 so Dio doesn't throw on the
      // redirect.
      final res = await api.get<void>(
        '/v1/commerce/affiliate/${widget.linkId}',
        options: Options(
          followRedirects: false,
          validateStatus: (code) => code != null && code >= 200 && code < 400,
        ),
      );
      final location = res.headers.value('location');
      if (location == null || location.isEmpty) {
        _fail('Empty redirect target.');
        return;
      }
      // location is /products/<product_id>?via=<affiliate_code>.
      // Parse the affiliate code, persist it, then route to the
      // in-app product detail.
      final uri = Uri.parse(location);
      final via = uri.queryParameters['via'];
      if (via != null && via.isNotEmpty) {
        ref.read(affiliateAttributionProvider.notifier).capture(via);
      }
      // Last path segment is the product id.
      final productId = uri.pathSegments.isNotEmpty
          ? uri.pathSegments.last
          : '';
      if (productId.isEmpty) {
        _fail('Redirect missing product id.');
        return;
      }
      if (!mounted) return;
      context.go('/commerce/product/$productId');
    } on DioException catch (e) {
      final status = e.response?.statusCode;
      if (status == 404) {
        _fail('This product is no longer available.');
      } else if (status == 410) {
        _fail('The creator has turned this affiliate link off.');
      } else if (status == 503) {
        _fail('Affiliate service is temporarily unavailable.');
      } else {
        _fail('Could not open product.');
      }
    } catch (_) {
      _fail('Could not open product.');
    }
  }

  void _fail(String msg) {
    if (!mounted) return;
    setState(() => _error = msg);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Opening product…')),
      body: Center(
        child: _error == null
            ? const CircularProgressIndicator()
            : Padding(
                padding: const EdgeInsets.symmetric(horizontal: 32),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.error_outline, size: 36, color: Colors.redAccent),
                    const SizedBox(height: 12),
                    Text(
                      _error!,
                      textAlign: TextAlign.center,
                      style: const TextStyle(color: Colors.black87),
                    ),
                    const SizedBox(height: 16),
                    TextButton(
                      onPressed: () => context.pop(),
                      child: const Text('Go back'),
                    ),
                  ],
                ),
              ),
      ),
    );
  }
}
