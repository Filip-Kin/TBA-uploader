import argparse
import os

import requests

TBA_KEY = os.environ['TBA_KEY']
TBA_URL = os.environ.get('TBA_URL', 'https://www.thebluealliance.com').rstrip('/')

def download_event_matches(event_key):
    response = requests.get(f'{TBA_URL}/api/v3/event/{event_key}/matches', headers={'X-TBA-Auth-Key': TBA_KEY})
    response.raise_for_status()
    return response.json()

def download_event_rankings(event_key):
    response = requests.get(f'{TBA_URL}/api/v3/event/{event_key}/rankings', headers={'X-TBA-Auth-Key': TBA_KEY})
    response.raise_for_status()
    return response.json()

def recursive_index(obj, key):
    value = obj
    for k in key:
        value = value[k]
    return value

def strip_frc_prefix(v):
    return v.removeprefix('frc')

class Field:
    needs_alliance = False
    def __init__(self, name, key, postprocess=lambda x:x):
        self.name = name
        if not isinstance(key, (list, tuple)):
            key = (key,)
        self.key = key
        self.postprocess = postprocess
    def get_value(self, record):
        try:
            return self.postprocess(recursive_index(record, self.key))
        except Exception as e:
            key = record.get('key', record.get('team_key', repr(record)))
            raise ValueError(f'Error parsing field {self!r} for record {key}') from e
    def curry_alliance(self, alliance):
        raise NotImplementedError
    def __repr__(self):
        return f'{type(self).__name__}(name={self.name!r}, key={self.key!r})'

class MatchField(Field):
    pass

class RankingField(Field):
    pass

class AllianceField(Field):
    needs_alliance = True
    alliance_field = 'alliances'
    def curry_alliance(self, alliance):
        return MatchField(alliance.capitalize() + ' ' + self.name, (self.alliance_field, alliance) + self.key, postprocess=self.postprocess)

class TeamNumberField(AllianceField):
    def __init__(self, name, team_index, *args, **kwargs):
        super().__init__(name, ('team_keys', team_index), postprocess=strip_frc_prefix, *args, **kwargs)

class BreakdownField(AllianceField):
    alliance_field = 'score_breakdown'

def record_sort_key(record):
    if 'comp_level' in record:
        return record['comp_level'], record['set_number'], record['match_number']
    elif 'rank' in record:
        return record['rank']
    raise ValueError('unknown object')

def parse_and_print_csv(records, field_defs) -> tuple[list[str], list[dict]]:
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

    for record in sorted(records, key=record_sort_key):
        if 'score_breakdown' in record and record['score_breakdown'] is None:
            continue
        print(','.join(str(f.get_value(record)) for f in all_fields))


year_fields = {
    2024: {
        "matches": [
            MatchField('Match', 'match_number'),
            [
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
        "rp": [
            RankingField('Rank', 'rank'),
            RankingField('Team', 'team_key', postprocess=strip_frc_prefix),
            RankingField('RP', ('sort_orders', 0)),
            RankingField('Avg Coop', ('sort_orders', 1)),
            RankingField('Avg Match', ('sort_orders', 2)),
            RankingField('Avg Auto', ('sort_orders', 3)),
            RankingField('Avg Stage', ('sort_orders', 4)),
            RankingField('Played', 'matches_played'),
        ],
    },
}

parser = argparse.ArgumentParser()
parser.add_argument('event_key')
parser.add_argument('-p', '--parser', default='matches')
args = parser.parse_args()

year = args.event_key[:4]
if year not in map(str, year_fields):
    raise ValueError(f'unsupported year: {year}')
year = int(year)

if args.parser.startswith('rp'):
    records = download_event_rankings(args.event_key)["rankings"]
else:
    records = download_event_matches(args.event_key)
parse_and_print_csv(records, year_fields[year][args.parser])
