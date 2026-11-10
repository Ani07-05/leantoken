import pytest

from leantoken import art


@pytest.mark.parametrize("scene", list(art.SCENE_ANIMAL))
def test_every_scene_renders_at_any_stage(scene):
    for t, grow in ((0, 0.2), (9, 1.0), (39, 0.6)):
        img = art.frame(scene, t, grow)
        assert img.size == (art.W * art.RENDER_SCALE, art.H * art.RENDER_SCALE)


def test_every_tool_maps_to_a_scene():
    assert set(art.TOOL_SCENE.values()) <= set(art.SCENE_ANIMAL)


def test_empty_shell_command_is_rejected_before_approval():
    from leantoken.tools import unrunnable

    assert unrunnable("  ").startswith("error: empty command")
