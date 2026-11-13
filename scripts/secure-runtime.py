#!/usr/bin/env python3
"""Construct immutable runtime/rollback configs, never start or stop services.

The future activation executor must bind these outputs to fresh preflight,
local image inspection, owner approval, backup and runtime acceptance. Image
IDs passed here are evidence inputs, never permission to pull/build/run.
"""
import copy
import re

IMAGE_ID = re.compile(r'^sha256:[a-f0-9]{64}$')


def freeze(config, images):
    """Pin active services to exact already-inspected local IDs, remove builds.

    Inactive optional profiles are removed rather than retaining latent mutable
    tags/builds that a later menu action could silently start. Active profiles
    need their own exact current image ID and are retained. Missing ordinary
    services, unknown IDs and dependencies on a removed service fail closed.
    """
    frozen = copy.deepcopy(config)
    services = frozen.get('services')
    if not isinstance(services, dict) or not services:
        raise ValueError('Resolved Compose service map required.')
    if set(images) - set(services):
        raise ValueError('Image identity has no matching Compose service.')
    disabled = []
    for name, service in list(services.items()):
        image = images.get(name)
        if image is None:
            if not service.get('profiles'):
                raise ValueError('An ordinary service has no verified immutable image: ' + name)
            disabled.append(name)
            del services[name]
            continue
        if not isinstance(image, str) or not IMAGE_ID.fullmatch(image):
            raise ValueError('Exact local immutable image ID required: ' + name)
        if service.get('scale', 1) != 1 or service.get('deploy', {}).get('replicas', 1) != 1:
            raise ValueError('Only single-node single-instance services are supported.')
        service['image'] = image
        service.pop('build', None)
        service['pull_policy'] = 'never'
        service.pop('develop', None)
        # Existing resolved bind sources must not be reinterpreted relative to
        # a new snapshot directory during rollback.
        for volume in service.get('volumes', []):
            if not isinstance(volume, dict):
                raise ValueError('Resolved mount definitions required.')
            if volume.get('type') == 'bind' and not str(volume.get('source', '')).startswith('/'):
                raise ValueError('Absolute resolved bind source required.')
    for service in services.values():
        dependencies = service.get('depends_on', {})
        if not isinstance(dependencies, (list, dict)) or set(dependencies) - set(services):
            raise ValueError('A selected service depends on an unverified or inactive service.')
        namespace = service.get('network_mode', '')
        if namespace.startswith('service:') and namespace.removeprefix('service:') not in services:
            raise ValueError('A selected namespace target is unavailable.')
    return frozen, sorted(disabled)


def pair(previous, candidate, current_images, proposed_images):
    """Only expected transport services may have a different candidate image.

    API/ingest/web/database and other existing application images must be the
    already-running versions. This prevents configuration migration from also
    becoming an unreviewed application/schema upgrade. Broker and secure-web
    must be independently inspected/pinned by the future executor.
    """
    if set(candidate.get('services', {})) - set(previous.get('services', {})) != {'secure-web'}:
        raise ValueError('Only the expected secure-web service may be added.')
    if set(previous['services']) - set(candidate['services']):
        raise ValueError('Configuration migration must not silently remove a service.')
    for name, image in current_images.items():
        if name != 'mosquitto' and proposed_images.get(name) != image:
            raise ValueError('Application image changed during transport migration: ' + name)
    old, old_disabled = freeze(previous, current_images)
    new, new_disabled = freeze(candidate, proposed_images)
    if old_disabled != new_disabled:
        raise ValueError('Inactive profile selection changed during transport migration.')
    return {'previous': old, 'candidate': new, 'disabled_profiles': old_disabled}
