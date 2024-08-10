import argparse
import os

import requests

TBA_KEY = os.environ['TBA_KEY']
TBA_URL = os.environ.get('TBA_URL', 'https://www.thebluealliance.com').rstrip('/')

def download_event_matches(event_key):
    response = requests.get(f'{TBA_URL}/api/v3/event/{event_key}/matches', headers={'X-TBA-Auth-Key': TBA_KEY})
    response.raise_for_status()
    return response.json()

def recursive_index(obj, key):
    value = obj
    for k in key:
        value = value[k]
    return value

class Field:
    needs_alliance = False
    def __init__(self, name, key, postprocess=lambda x:x):
        self.name = name
        if not isinstance(key, (list, tuple)):
            key = (key,)
        self.key = key
        self.postprocess = postprocess
    def get_value(self, match):
        try:
            return self.postprocess(recursive_index(match, self.key))
        except Exception as e:
            print('xxx', match)
            raise ValueError(f'Error parsing field {self!r} for match {match["key"]}') from e
    def curry_alliance(self, alliance):
        raise NotImplementedError
    def __repr__(self):
        return f'{type(self).__name__}(name={self.name!r}, key={self.key!r})'

class MatchField(Field):
    pass

class AllianceField(Field):
    needs_alliance = True
    alliance_field = 'alliances'
    def curry_alliance(self, alliance):
        return MatchField(alliance.capitalize() + ' ' + self.name, (self.alliance_field, alliance) + self.key, postprocess=self.postprocess)

class TeamNumberField(AllianceField):
    def __init__(self, name, team_index, *args, **kwargs):
        super().__init__(name, ('team_keys', team_index), postprocess=lambda v: v.removeprefix('frc'), *args, **kwargs)

class BreakdownField(AllianceField):
    alliance_field = 'score_breakdown'

def match_sort_key(match):
    return match['comp_level'], match['set_number'], match['match_number']

def parse_match_fields(matches, field_defs) -> tuple[list[str], list[dict]]:
    all_fields = []
    for field_group in field_defs:
        if isinstance(field_group, Field):
            field_group = [field_group]

        fields = []
        alliance_fields = {'red': [], 'blue': []}
        for field in field_group:
            if field.needs_alliance:
                for alliance in alliance_fields:
                    alliance_fields[alliance].append(field.curry_alliance(alliance))
            else:
                fields.append(field)

        all_fields.extend(fields + alliance_fields['red'] + alliance_fields['blue'])

    print(','.join(f.name for f in all_fields))

    for match in sorted(matches, key=match_sort_key):
        if match['score_breakdown'] is None:
            continue
        print(','.join(str(f.get_value(match)) for f in all_fields))


year_fields = {
    2024: [
        MatchField('Match', 'match_number'),
        [
            # AllianceField('1', ('team_keys', 0), postprocess=lambda v: v.removeprefix('frc')),
            # AllianceField('2', ('team_keys', 1), postprocess=lambda v: v.removeprefix('frc')),
            # AllianceField('3', ('team_keys', 2), postprocess=lambda v: v.removeprefix('frc')),
            TeamNumberField('1', 0),
            TeamNumberField('2', 1),
            TeamNumberField('3', 2),
        ],
        [
            BreakdownField('RP', 'rp'),
            BreakdownField('Score', 'totalPoints'),
            BreakdownField('Fouls', 'foulPoints'),
            BreakdownField('Auto', 'autoPoints'),
            BreakdownField('Stage', 'endGameTotalStagePoints'),
        ],
    ],
}

parser = argparse.ArgumentParser()
parser.add_argument('event_key')
args = parser.parse_args()

year = args.event_key[:4]
if year not in map(str, year_fields):
    raise ValueError(f'unsupported year: {year}')
year = int(year)

matches = download_event_matches(args.event_key)
parse_match_fields(matches, year_fields[year])
