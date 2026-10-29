import copy
import unittest
from ci_plan import choose, gate


class ValidationPlan(unittest.TestCase):
    def test_development_uses_two_current_platforms(self):
        plan = choose('pull_request', 'refs/pull/1/merge', 'auto', ['tunnel.go'])
        self.assertEqual(plan['mode'], 'quick')
        self.assertEqual(len(plan['matrix']['include']), 2)
        self.assertFalse(plan['quality'])

    def test_main_and_tags_cannot_be_lowered(self):
        for ref in ('refs/heads/main', 'refs/heads/v2', 'refs/tags/v0.1.3'):
            plan = choose('workflow_dispatch', ref, 'quick', ['tunnel.go'])
            self.assertEqual(plan['mode'], 'candidate')
            self.assertEqual(len(plan['matrix']['include']), 6)
            self.assertTrue(plan['quality'])

    def test_docs_skip_go_but_tags_still_validate(self):
        self.assertEqual(choose('push', 'refs/heads/main', 'auto', ['README.md'])['mode'], 'docs')
        self.assertEqual(choose('push', 'refs/tags/v0.1.3', 'auto', ['README.md'])['mode'], 'candidate')
        self.assertEqual(choose('workflow_dispatch', 'refs/heads/dev', 'candidate', ['README.md'])['mode'], 'candidate')

    def test_unknown_missing_or_empty_input_is_conservative(self):
        for paths in (None, [], ['new-tool.bin']):
            self.assertEqual(choose('push', 'refs/heads/dev', 'quick', paths)['mode'], 'candidate')

    def test_gate_rejects_every_unexpected_job_state(self):
        for mode in ('docs', 'coordination', 'quick', 'candidate'):
            plan = dict(mode=mode, quality=mode == 'candidate')
            jobs = {name: dict(result='success' if required else 'skipped') for name, required in {
                'plan': True, 'dependency-policy': mode in ('quick', 'candidate'), 'test': mode in ('quick', 'candidate'), 'quality': mode == 'candidate'}.items()}
            gate(plan, jobs)
            for name in jobs:
                for status in ('failure', 'cancelled', None, 'skipped' if jobs[name]['result'] == 'success' else 'success'):
                    broken = copy.deepcopy(jobs); broken[name]['result'] = status
                    with self.assertRaises(ValueError): gate(plan, broken)
                broken = copy.deepcopy(jobs); del broken[name]
                with self.assertRaises(ValueError): gate(plan, broken)

    def test_invalid_requested_mode_is_rejected(self):
        with self.assertRaises(ValueError): choose('push', 'refs/heads/dev', 'unknown', ['x.go'])

    def test_tagged_coordination_changes_reuse_native_inputs(self):
        paths=['integration/policy/policy_e2e_test.go', 'scripts/ci_plan.py', '.github/workflows/ci.yml']
        plan=choose('push', 'refs/heads/main', 'auto', paths)
        self.assertEqual(plan['mode'], 'coordination')
        self.assertFalse(plan['quality'])
        self.assertEqual(choose('push', 'refs/heads/main', 'auto', paths+['go.mod'])['mode'], 'candidate')
        self.assertEqual(choose('push', 'refs/tags/v0.1.3', 'auto', paths)['mode'], 'candidate')
        self.assertEqual(choose('workflow_dispatch', 'refs/heads/main', 'candidate', paths)['mode'], 'candidate')

    def test_unknown_plan_cannot_authorize_skips(self):
        with self.assertRaises(ValueError): gate(dict(mode='unknown', quality=False), {})
        with self.assertRaises(ValueError): gate(dict(mode='candidate', quality=False), {})


if __name__ == '__main__': unittest.main()
