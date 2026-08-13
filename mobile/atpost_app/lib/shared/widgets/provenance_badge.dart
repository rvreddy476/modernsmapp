import 'package:flutter/material.dart';

class ProvenanceBadge extends StatelessWidget {
  const ProvenanceBadge({super.key});

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: 'Creator disclosed AI-generated or significantly altered content',
      // `excludeSemantics: true` IS THE LOAD-BEARING PROPERTY. Verified by
      // isolating each: removing excludeSemantics fails
      // module_07_safety_truth_test; removing container does not.
      //
      // Without it, the child Text contributes its own semantics node
      // ("AI / altered content") and the parent's full disclosure sentence
      // never becomes a findable node. A screen-reader user still heard *a*
      // disclosure, so nothing looked broken from the outside — but the
      // wording the policy requires never reached them.
      //
      // `container: true` is defensive, not proven by that test: it keeps this
      // badge a distinct node so its label cannot be merged away into an
      // ancestor card's semantics when it is rendered inside a tappable feed
      // card. Stated separately so nobody reads the passing test as evidence
      // for it.
      container: true,
      excludeSemantics: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
        decoration: BoxDecoration(
          color: const Color(0xCC241D0B),
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: const Color(0xFFE6B94A)),
        ),
        child: const Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.auto_awesome_outlined,
              size: 14,
              color: Color(0xFFFFD56A),
            ),
            SizedBox(width: 5),
            Text(
              'AI / altered content',
              style: TextStyle(
                color: Color(0xFFFFE6A6),
                fontSize: 11,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
